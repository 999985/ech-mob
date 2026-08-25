package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/dns/dnsmessage"
)

// ======================== 全局参数与配置 ========================

var (
	listenAddr  string
	serverAddr  string
	serverIP    string
	token       string
	dnsServer   string
	echDomain   string
	routingMode string
	useFakeIP   bool
	dataDir     string
	dataDirMu   sync.RWMutex

	// ECH 相关缓存
	echListMu    sync.RWMutex
	echList      []byte
	lastECHFetch time.Time

	// 极速大招 3: 将应用层拷贝缓冲区从 32KB 提升至 64KB，吃满大带宽
	bufPool = sync.Pool{
		New: func() interface{} { return make([]byte, 64*1024) },
	}

	// 提速 1: DNS 缓存机制
	dnsCache   sync.Map // map[string]dnsCacheItem
	dnsCacheMu sync.RWMutex

	// 提速 2: 高性能域名字典树 (带热重载锁)
	domainTrie   = &trieNode{children: make(map[string]*trieNode)}
	domainTrieMu sync.RWMutex

	// GeoIP 路由表
	chinaIPv4Ranges []ipRange
	chinaIPv6Ranges []ip6Range
	ipRangesMu      sync.RWMutex
)

// 极速大招 1: 0-RTT 预热连接池
type wsConnItem struct {
	conn      *websocket.Conn
	createdAt time.Time
}

var (
	wsPool     chan wsConnItem
	wsPoolSize = 15 // 预先在后台建立 15 个连接备用
)

type ipRange struct{ start, end uint32 }
type ip6Range struct{ startHigh, startLow, endHigh, endLow uint64 }
type dnsCacheItem struct {
	addr      netip.Addr
	expiresAt time.Time
}

type trieNode struct {
	children map[string]*trieNode
	isEnd    bool
}

func init() {
	flag.StringVar(&listenAddr, "l", "127.0.0.1:30000", "本地监听地址")
	flag.StringVar(&serverAddr, "f", "", "Worker 地址")
	flag.StringVar(&serverIP, "ip", "", "指定 Worker IP (支持逗号分隔多 IP 容灾)")
	flag.StringVar(&token, "token", "1", "暗号数字")
	flag.StringVar(&dnsServer, "dns", "https://dns.alidns.com/dns-query", "DoH 接口")
	flag.StringVar(&echDomain, "ech", "cloudflare-ech.com", "ECH 域名")
	flag.StringVar(&routingMode, "routing", "bypass_cn", "分流模式")
	flag.BoolVar(&useFakeIP, "fakeip", false, "是否开启 Fake-IP 本地 DNS")

	wsPool = make(chan wsConnItem, wsPoolSize)
}

// ======================== 核心系统初始化 ========================

func ignoredMain() {
	flag.Parse()
	if serverAddr == "" {
		log.Fatal("必须指定服务端地址 -f")
	}

	log.Printf("[启动] 正在初始化 ECH...")
	_ = prepareECH()
	ctx := context.Background()
	go startECHAutoRefresh(ctx)
	go cleanDNSCacheLoop(ctx)

	if routingMode == "bypass_cn" {
		log.Printf("[启动] 正在加载 Geosite 与 GeoIP 规则...")
		if err := loadRoutingRules(); err != nil {
			log.Printf("[警告] 路由加载失败: %v", err)
		} else {
			log.Printf("[启动] 路由加载完毕 (IPv4: %d, IPv6: %d)", len(chinaIPv4Ranges), len(chinaIPv6Ranges))
			go updateRoutingRulesTask(ctx) // 启动每日自动更新规则
		}
	}

	initNodeManager(ctx, serverIP)
	go startWSPoolManager(ctx) // 极速大招 1: 启动 0-RTT 后台建连

	if useFakeIP {
		go startFakeIPServer(ctx)
	}

	runProxyServer(listenAddr)
}

// ======================== 极速大招 1：0-RTT 连接池 ========================

func startWSPoolManager(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if len(wsPool) < cap(wsPool) {
			ws, err := dialWebSocketWithECH(1)
			if err == nil {
				select {
				case wsPool <- wsConnItem{conn: ws, createdAt: time.Now()}:
				case <-ctx.Done():
					_ = ws.Close()
					return
				}
			} else {
				if !waitForContext(ctx, 500*time.Millisecond) {
					return
				}
			}
		} else {
			if !waitForContext(ctx, 100*time.Millisecond) {
				return
			}
		}
	}
}

func waitForContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func getFastWSConn() (*websocket.Conn, error) {
	for {
		select {
		case item := <-wsPool:
			// Cloudflare 会清清理空闲过久的连接，仅使用 45 秒内的新鲜连接
			if time.Since(item.createdAt) < 45*time.Second {
				return item.conn, nil
			}
			_ = item.conn.Close()
		default:
			// 备用池耗尽，现场拨号
			return dialWebSocketWithECH(1)
		}
	}
}

// ======================== 进阶 3：底层热备容灾 (TLS精准探活) ========================

type proxyNode struct {
	IP    string
	Lat   time.Duration
	Fails int
}

var (
	nodePool []*proxyNode
	nodeMu   sync.RWMutex
)

func initNodeManager(ctx context.Context, ips string) {
	nodeMu.Lock()
	nodePool = nil
	for _, ip := range strings.Split(ips, ",") {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			nodePool = append(nodePool, &proxyNode{IP: ip, Lat: 100 * time.Millisecond})
		}
	}
	nodeCount := len(nodePool)
	nodeMu.Unlock()
	if nodeCount > 1 {
		log.Printf("[容灾] 成功建立容灾池，包含 %d 个节点", nodeCount)
		go nodePingLoop(ctx)
	}
}

func getBestNode() *proxyNode {
	nodeMu.RLock()
	defer nodeMu.RUnlock()
	var best *proxyNode
	for _, n := range nodePool {
		if n.Fails > 3 {
			continue // 暂时屏蔽故障节点
		}
		if best == nil || n.Lat < best.Lat {
			best = n
		}
	}
	if best == nil && len(nodePool) > 0 {
		return nodePool[0]
	}
	return best
}

func reportNodeResult(n *proxyNode, success bool) {
	if n == nil {
		return
	}
	nodeMu.Lock()
	defer nodeMu.Unlock()
	if success {
		n.Fails = 0
	} else {
		n.Fails++
	}
}

// 进阶：精准 TLS 探活，防止 TCP 伪墙
func nodePingLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	host, _, _, _ := parseServerAddr(serverAddr)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		nodeMu.RLock()
		nodes := make([]*proxyNode, len(nodePool))
		copy(nodes, nodePool)
		nodeMu.RUnlock()
		echBytes, _ := getECHList()

		for _, n := range nodes {
			go func(node *proxyNode) {
				start := time.Now()
				conn, err := net.DialTimeout("tcp", net.JoinHostPort(node.IP, "443"), 3*time.Second)
				if err != nil {
					reportNodeResult(node, false)
					return
				}
				defer conn.Close()

				tlsCfg, err := buildTLSConfigWithECH(host, echBytes)
				if err != nil {
					return
				}
				tlsConn := tls.Client(conn, tlsCfg)
				tlsConn.SetDeadline(time.Now().Add(3 * time.Second))

				if err := tlsConn.Handshake(); err == nil {
					nodeMu.Lock()
					node.Lat = time.Since(start)
					node.Fails = 0
					nodeMu.Unlock()
				} else {
					reportNodeResult(node, false)
				}
			}(n)
		}
	}
}

// ======================== 进阶 2：Fake-IP 本地极速 DNS (持久化+IPv6) ========================

var (
	fakeIPMap    sync.Map              // string(domain) -> uint32(IP)
	reverseIPMap sync.Map              // uint32(IP) -> string(domain)
	currentIP    uint32   = 0xC6120001 // 198.18.0.1
	fakeIPMutex  sync.Mutex
)

type fakeIPCache struct {
	CurrentIP uint32            `json:"current_ip"`
	DomainMap map[string]uint32 `json:"domain_map"`
}

func loadFakeIPCache() {
	data, err := os.ReadFile(dataPath("fakeip_cache.json"))
	if err != nil {
		return
	}
	var cache fakeIPCache
	if err := json.Unmarshal(data, &cache); err == nil {
		fakeIPMutex.Lock()
		currentIP = cache.CurrentIP
		for domain, ip := range cache.DomainMap {
			fakeIPMap.Store(domain, ip)
			reverseIPMap.Store(ip, domain)
		}
		fakeIPMutex.Unlock()
		log.Printf("[Fake-IP] 成功加载 %d 条缓存记录", len(cache.DomainMap))
	}
}

func saveFakeIPCache() {
	fakeIPMutex.Lock()
	defer fakeIPMutex.Unlock()
	cache := fakeIPCache{
		CurrentIP: currentIP,
		DomainMap: make(map[string]uint32),
	}
	fakeIPMap.Range(func(key, value interface{}) bool {
		cache.DomainMap[key.(string)] = value.(uint32)
		return true
	})
	if data, err := json.MarshalIndent(cache, "", "  "); err == nil {
		_ = os.WriteFile(dataPath("fakeip_cache.json"), data, 0644)
	}
}

func getOrAllocateFakeIP(domain string) []byte {
	var allocated uint32
	if val, ok := fakeIPMap.Load(domain); ok {
		allocated = val.(uint32)
	} else {
		fakeIPMutex.Lock()
		if val, ok := fakeIPMap.Load(domain); ok {
			allocated = val.(uint32)
		} else {
			allocated = currentIP
			currentIP++
			if currentIP > 0xC612FFFF {
				currentIP = 0xC6120001
			}
			fakeIPMap.Store(domain, allocated)
			reverseIPMap.Store(allocated, domain)
		}
		fakeIPMutex.Unlock()
	}
	ip := make([]byte, 4)
	binary.BigEndian.PutUint32(ip, allocated)
	return ip
}

// 进阶：生成特定的 Fake-IPv6 地址 (fd00::xxxx:xxxx)
func fakeIPv4ToIPv6(ip4 []byte) [16]byte {
	var ip6 [16]byte
	ip6[0] = 0xfd
	ip6[1] = 0x00
	ip6[12] = ip4[0]
	ip6[13] = ip4[1]
	ip6[14] = ip4[2]
	ip6[15] = ip4[3]
	return ip6
}

// 同时还原 IPv4 和 IPv6 伪造地址
func resolveFakeIP(targetHost string) string {
	if addr, err := netip.ParseAddr(targetHost); err == nil {
		if addr.Is4() {
			val := binary.BigEndian.Uint32(addr.AsSlice())
			if val >= 0xC6120000 && val <= 0xC613FFFF {
				if domain, ok := reverseIPMap.Load(val); ok {
					return domain.(string)
				}
			}
		} else if addr.Is6() {
			v6 := addr.As16()
			if v6[0] == 0xfd && v6[1] == 0x00 {
				val := binary.BigEndian.Uint32(v6[12:16])
				if val >= 0xC6120000 && val <= 0xC613FFFF {
					if domain, ok := reverseIPMap.Load(val); ok {
						return domain.(string)
					}
				}
			}
		}
	}
	return targetHost
}

func startFakeIPServer(ctx context.Context) {
	loadFakeIPCache()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				saveFakeIPCache()
			}
		}
	}()

	pc, err := net.ListenPacket("udp", "127.0.0.1:53")
	if err != nil {
		log.Printf("[Fake-IP] 无法监听 53 端口: %v", err)
		return
	}
	log.Printf("[Fake-IP] 本地极速 DNS 服务已启动 (127.0.0.1:53)")
	defer pc.Close()
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	buf := make([]byte, 512)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		queryData := append([]byte(nil), buf[:n]...)
		go handleDNSQuery(pc, addr, queryData)
	}
}

func handleDNSQuery(pc net.PacketConn, addr net.Addr, queryData []byte) {
	var p dnsmessage.Parser
	header, err := p.Start(queryData)
	if err != nil {
		return
	}
	q, err := p.Question()
	if err != nil {
		return
	}

	var answers []dnsmessage.Resource
	// 彻底接管系统 DNS，切断 AAAA 等待超时
	if q.Type == dnsmessage.TypeA || q.Type == dnsmessage.TypeAAAA {
		domain := strings.TrimSuffix(q.Name.String(), ".")
		fakeIPBytes := getOrAllocateFakeIP(domain)

		if q.Type == dnsmessage.TypeA {
			answers = append(answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1},
				Body:   &dnsmessage.AResource{A: [4]byte{fakeIPBytes[0], fakeIPBytes[1], fakeIPBytes[2], fakeIPBytes[3]}},
			})
		} else {
			answers = append(answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 1},
				Body:   &dnsmessage.AAAAResource{AAAA: fakeIPv4ToIPv6(fakeIPBytes)},
			})
		}
	}

	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: header.ID, Response: true, OpCode: header.OpCode, Authoritative: true,
		RecursionDesired: header.RecursionDesired, RecursionAvailable: true, RCode: dnsmessage.RCodeSuccess,
	})
	_ = builder.StartQuestions()
	_ = builder.Question(q)
	_ = builder.StartAnswers()

	for _, ans := range answers {
		if aRes, ok := ans.Body.(*dnsmessage.AResource); ok {
			_ = builder.AResource(ans.Header, *aRes)
		}
		if a6Res, ok := ans.Body.(*dnsmessage.AAAAResource); ok {
			_ = builder.AAAAResource(ans.Header, *a6Res)
		}
	}
	msg, _ := builder.Finish()
	_, _ = pc.WriteTo(msg, addr)
}

// ======================== 核心隧道逻辑 ========================

func handleTunnel(conn net.Conn, target, clientAddr string, mode int, firstFrame string) error {
	targetHost, port, _ := net.SplitHostPort(target)
	if targetHost == "" {
		targetHost = target
	}

	if useFakeIP {
		realDomain := resolveFakeIP(targetHost)
		if realDomain != targetHost {
			targetHost = realDomain
			target = net.JoinHostPort(realDomain, port)
		}
	}

	if shouldBypassProxy(targetHost) {
		return handleDirectConnection(conn, target, clientAddr, mode, firstFrame)
	}

	// 极速大招 3：拉满本地 TCP 窗口至 256KB
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetReadBuffer(256 * 1024)
		tcpConn.SetWriteBuffer(256 * 1024)
	}

	// 极速大招 1：秒级获取 WebSocket，实现 0-RTT
	wsConn, dialErr := getFastWSConn()
	if dialErr != nil {
		sendErrorResponse(conn, mode)
		return dialErr
	}
	defer wsConn.Close()

	var wsWriteMu sync.Mutex
	writeWS := func(msgType int, data []byte) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		wsConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		err := wsConn.WriteMessage(msgType, data)
		wsConn.SetWriteDeadline(time.Time{})
		return err
	}

	firstPayload := []byte(fmt.Sprintf("CONNECT:%s\n", target))
	if firstFrame != "" {
		firstPayload = append(firstPayload, []byte(firstFrame)...)
	}
	if err := writeWS(websocket.BinaryMessage, firstPayload); err != nil {
		sendErrorResponse(conn, mode)
		return err
	}

	sendSuccessResponse(conn, mode)
	done := make(chan error, 2)
	stopKeepAlive := make(chan struct{})
	defer close(stopKeepAlive)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = writeWS(websocket.PingMessage, nil)
			case <-stopKeepAlive:
				return
			}
		}
	}()

	go func() {
		for {
			buf := bufPool.Get().([]byte)
			n, err := conn.Read(buf)
			if n > 0 {
				if errWS := writeWS(websocket.BinaryMessage, buf[:n]); errWS != nil {
					bufPool.Put(buf)
					done <- errWS
					return
				}
			}
			bufPool.Put(buf)
			if err != nil {
				done <- err
				return
			}
		}
	}()

	go func() {
		for {
			mt, msg, err := wsConn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			if mt == websocket.BinaryMessage && len(msg) > 0 {
				if _, err := conn.Write(msg); err != nil {
					done <- err
					return
				}
			}
		}
	}()

	return <-done
}

// ======================== 基础代理与 ECH 函数 ========================

func runProxyServer(addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}
	log.Printf("[代理] 服务器就绪: %s", addr)
	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return
	}
	conn.SetReadDeadline(time.Time{})
	if buf[0] == 0x05 {
		handleSOCKS5(conn, conn.RemoteAddr().String(), buf[0])
	} else {
		handleHTTP(conn, conn.RemoteAddr().String(), buf[0])
	}
}

func handleSOCKS5(conn net.Conn, clientAddr string, firstByte byte) {
	nMethodsBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, nMethodsBuf); err != nil {
		return
	}
	methods := make([]byte, int(nMethodsBuf[0]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	conn.Write([]byte{0x05, 0x00})
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if header[1] != 0x01 {
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	var target string
	switch header[3] {
	case 0x01:
		buf := make([]byte, 4)
		io.ReadFull(conn, buf)
		target = net.IP(buf).String()
	case 0x03:
		buf := make([]byte, 1)
		io.ReadFull(conn, buf)
		domainLen := int(buf[0])
		domainBuf := make([]byte, domainLen)
		io.ReadFull(conn, domainBuf)
		target = string(domainBuf)
	case 0x04:
		buf := make([]byte, 16)
		io.ReadFull(conn, buf)
		target = net.IP(buf).String()
	}
	portBuf := make([]byte, 2)
	io.ReadFull(conn, portBuf)
	port := int(portBuf[0])<<8 | int(portBuf[1])
	target = fmt.Sprintf("%s:%d", target, port)
	handleTunnel(conn, target, clientAddr, 1, "")
}

func handleHTTP(conn net.Conn, clientAddr string, firstByte byte) {
	reader := bufio.NewReader(io.MultiReader(strings.NewReader(string(firstByte)), conn))
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	target := req.Host
	if !strings.Contains(target, ":") {
		target += ":80"
	}
	if req.Method == "CONNECT" {
		handleTunnel(conn, target, clientAddr, 2, "")
	} else {
		var buf bytes.Buffer
		req.Write(&buf)
		handleTunnel(conn, target, clientAddr, 3, buf.String())
	}
}

func handleDirectConnection(conn net.Conn, target, clientAddr string, mode int, firstFrame string) error {
	targetConn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		sendErrorResponse(conn, mode)
		return err
	}
	if tConn, ok := targetConn.(*net.TCPConn); ok {
		tConn.SetNoDelay(true)
		tConn.SetReadBuffer(256 * 1024) // 极速大招 3：拉满直连缓冲区
		tConn.SetWriteBuffer(256 * 1024)
	}
	defer targetConn.Close()
	sendSuccessResponse(conn, mode)
	if firstFrame != "" {
		targetConn.Write([]byte(firstFrame))
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(targetConn, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, targetConn); done <- struct{}{} }()
	<-done
	return nil
}

func sendErrorResponse(conn net.Conn, mode int) {
	if mode == 1 {
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	} else {
		conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
	}
}
func sendSuccessResponse(conn net.Conn, mode int) {
	if mode == 1 {
		conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	} else if mode == 2 {
		conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	}
}

func dialWebSocketWithECH(maxRetries int) (*websocket.Conn, error) {
	host, port, path, err := parseServerAddr(serverAddr)
	if err != nil {
		return nil, err
	}
	wsURL := "wss://" + net.JoinHostPort(host, port) + path
	targetNode := getBestNode()

	for attempt := 1; attempt <= maxRetries; attempt++ {
		echBytes, err := getECHList()
		if err != nil {
			_ = prepareECH()
			continue
		}
		tlsCfg, _ := buildTLSConfigWithECH(host, echBytes)

		// 极速大招 3：加大 Dialer 底层 Buffer，提高 WebSocket 拆包吞吐上限
		dialer := websocket.Dialer{
			TLSClientConfig:  tlsCfg,
			HandshakeTimeout: 5 * time.Second,
			ReadBufferSize:   256 * 1024,
			WriteBufferSize:  256 * 1024,
			Subprotocols:     []string{token},
		}
		dialer.NetDial = func(network, address string) (net.Conn, error) {
			tAddr := net.JoinHostPort(host, port)
			if targetNode != nil {
				tAddr = nodeAddress(targetNode.IP, port)
			}
			conn, err := net.DialTimeout(network, tAddr, 5*time.Second)
			if err == nil {
				if tcpConn, ok := conn.(*net.TCPConn); ok {
					tcpConn.SetNoDelay(true)
					tcpConn.SetReadBuffer(256 * 1024) // 极速大招 3
					tcpConn.SetWriteBuffer(256 * 1024)
				}
			}
			return conn, err
		}
		wsConn, _, dialErr := dialer.Dial(wsURL, nil)
		if dialErr != nil {
			reportNodeResult(targetNode, false)
			if strings.Contains(dialErr.Error(), "ECH") || strings.Contains(dialErr.Error(), "handshake") {
				_ = prepareECH()
			}
			targetNode = getBestNode()
			continue
		}
		reportNodeResult(targetNode, true)
		return wsConn, nil
	}
	return nil, errors.New("容灾池节点全部连接超时")
}

func parseServerAddr(addr string) (host, port, path string, err error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "", "", errors.New("Worker 地址不能为空")
	}
	if !strings.Contains(addr, "://") {
		addr = "wss://" + addr
	}

	parsed, err := url.Parse(addr)
	if err != nil {
		return "", "", "", fmt.Errorf("解析 Worker 地址: %w", err)
	}
	if parsed.Scheme != "wss" && parsed.Scheme != "https" {
		return "", "", "", fmt.Errorf("Worker 地址必须使用 wss://: %s", parsed.Scheme)
	}
	host = parsed.Hostname()
	if host == "" {
		return "", "", "", errors.New("Worker 地址缺少主机名")
	}
	port = parsed.Port()
	if port == "" {
		port = "443"
	}
	path = parsed.RequestURI()
	if path == "" {
		path = "/"
	}
	return host, port, path, nil
}

func nodeAddress(ip, port string) string {
	ip = strings.TrimSpace(ip)
	if _, _, err := net.SplitHostPort(ip); err == nil {
		return ip
	}
	return net.JoinHostPort(strings.Trim(ip, "[]"), port)
}

type preferredIPScore struct {
	ip    string
	score time.Duration
}

// parsePreferredIPs accepts the same comma/newline format as the desktop
// client and ignores inline comments after '#'.
func parsePreferredIPs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	})
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		ip := strings.TrimSpace(strings.SplitN(part, "#", 2)[0])
		if ip == "" {
			continue
		}
		if _, exists := seen[ip]; exists {
			continue
		}
		seen[ip] = struct{}{}
		result = append(result, ip)
	}
	return result
}

func probePreferredIP(parent context.Context, ip string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	start := time.Now()
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", nodeAddress(ip, "443"))
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

// rankPreferredIPs mirrors main.py's startup smart selector: probe candidates
// concurrently, score latency plus jitter, and retain the fastest three.
func rankPreferredIPs(parent context.Context, raw string, limit int) []string {
	ips := parsePreferredIPs(raw)
	if len(ips) <= 1 || limit <= 0 {
		return ips
	}
	type probeResult struct {
		ip      string
		samples []time.Duration
	}
	results := make(chan probeResult, len(ips))
	var wg sync.WaitGroup
	for _, ip := range ips {
		wg.Add(1)
		go func(candidate string) {
			defer wg.Done()
			samples := make([]time.Duration, 0, 3)
			for attempt := 0; attempt < 3; attempt++ {
				latency, err := probePreferredIP(parent, candidate)
				if err == nil {
					samples = append(samples, latency)
				}
				if attempt < 2 {
					if !waitForContext(parent, 40*time.Millisecond) {
						return
					}
				}
			}
			if len(samples) > 0 {
				results <- probeResult{ip: candidate, samples: samples}
			}
		}(ip)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	scores := make([]preferredIPScore, 0, len(ips))
	for result := range results {
		var total time.Duration
		for _, sample := range result.samples {
			total += sample
		}
		average := total / time.Duration(len(result.samples))
		var variance float64
		for _, sample := range result.samples {
			delta := float64(sample - average)
			variance += delta * delta
		}
		jitter := time.Duration(0)
		if len(result.samples) > 1 {
			jitter = time.Duration(math.Sqrt(variance / float64(len(result.samples)-1)))
		}
		scores = append(scores, preferredIPScore{ip: result.ip, score: average + jitter/2})
	}
	if len(scores) == 0 {
		log.Printf("[优选] 所有候选 IP 测速失败，保留原始节点池")
		return ips
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].score < scores[j].score })
	if limit > len(scores) {
		limit = len(scores)
	}
	selected := make([]string, 0, limit)
	for _, score := range scores[:limit] {
		selected = append(selected, score.ip)
	}
	log.Printf("[优选] 智能测速完成，节点池: %s", strings.Join(selected, ","))
	return selected
}

func normalizeDoHURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("ECH DNS 地址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("解析 ECH DNS 地址: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", errors.New("ECH DNS 地址必须是有效的 https:// 地址")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/dns-query"
	}
	return parsed.String(), nil
}

func queryHTTPSRecord(domain, dnsServer string) (string, error) {
	u, _ := url.Parse(dnsServer)
	dnsQuery := append([]byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0}, buildQName(domain)...)
	dnsQuery = append(dnsQuery, 0, 65, 0, 1)
	q := u.Query()
	q.Set("dns", base64.RawURLEncoding.EncodeToString(dnsQuery))
	u.RawQuery = q.Encode()
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.Header.Set("Accept", "application/dns-message")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return parseHTTPSRecord(body), nil
}

func parseHTTPSRecord(data []byte) string {
	for i := 0; i < len(data)-4; i++ {
		if data[i] == 0x00 && data[i+1] == 0x05 {
			ln := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
			if i+4+ln <= len(data) {
				return base64.StdEncoding.EncodeToString(data[i+4 : i+4+ln])
			}
		}
	}
	return ""
}

func buildQName(domain string) []byte {
	var buf []byte
	for _, part := range strings.Split(domain, ".") {
		buf = append(buf, byte(len(part)))
		buf = append(buf, part...)
	}
	return append(buf, 0)
}

func buildTLSConfigWithECH(serverName string, echList []byte) (*tls.Config, error) {
	roots, _ := x509.SystemCertPool()
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: serverName, RootCAs: roots}
	val := reflect.ValueOf(cfg).Elem()
	f := val.FieldByName("EncryptedClientHelloConfigList")
	if f.IsValid() && f.CanSet() {
		f.Set(reflect.ValueOf(echList))
	}
	f2 := val.FieldByName("EncryptedClientHelloRejectionVerify")
	if f2.IsValid() && f2.CanSet() {
		f2.Set(reflect.ValueOf(func(cs tls.ConnectionState) error { return errors.New("ECH 被拒绝") }))
	}
	return cfg, nil
}

func prepareECH() error {
	dohServers := []string{"https://dns.alidns.com/dns-query", "https://doh.pub/dns-query", "https://doh.360.cn/dns-query"}
	if dnsServer != "" && dnsServer != "https://dns.alidns.com/dns-query" {
		dohServers = append([]string{dnsServer}, dohServers...)
	}
	var lastErr error
	for _, ds := range dohServers {
		if ds == "" {
			continue
		}
		echBase64, err := queryHTTPSRecord(echDomain, ds)
		if err == nil && echBase64 != "" {
			raw, err := base64.StdEncoding.DecodeString(echBase64)
			if err == nil {
				echListMu.Lock()
				echList = raw
				lastECHFetch = time.Now()
				echListMu.Unlock()
				return nil
			}
		}
		lastErr = err
	}
	return lastErr
}

func getECHList() ([]byte, error) {
	echListMu.RLock()
	if len(echList) > 0 {
		data := echList
		echListMu.RUnlock()
		return data, nil
	}
	echListMu.RUnlock()
	if err := prepareECH(); err != nil {
		return nil, err
	}
	echListMu.RLock()
	defer echListMu.RUnlock()
	return echList, nil
}

func startECHAutoRefresh(ctx context.Context) {
	ticker := time.NewTicker(55 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = prepareECH()
		}
	}
}

// ======================== Geosite / GeoIP 路由引擎 (带热重载) ========================

func insertToTrie(root *trieNode, domain string) {
	parts := strings.Split(strings.ToLower(domain), ".")
	node := root
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if _, exists := node.children[part]; !exists {
			node.children[part] = &trieNode{children: make(map[string]*trieNode)}
		}
		node = node.children[part]
	}
	node.isEnd = true
}

func matchDomain(domain string) bool {
	domainTrieMu.RLock()
	defer domainTrieMu.RUnlock()

	parts := strings.Split(strings.ToLower(domain), ".")
	node := domainTrie
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		nextNode, exists := node.children[part]
		if !exists {
			return false
		}
		if nextNode.isEnd {
			return true
		}
		node = nextNode
	}
	return false
}

func shouldBypassProxy(targetHost string) bool {
	if routingMode == "none" {
		return true
	}
	if routingMode == "global" {
		return false
	}
	if routingMode != "bypass_cn" {
		return false
	}

	if matchDomain(targetHost) {
		return true
	}
	if addr, err := netip.ParseAddr(targetHost); err == nil {
		return isChinaIP(addr)
	}

	if cachedAddr, ok := dnsCache.Load(targetHost); ok {
		item := cachedAddr.(dnsCacheItem)
		if time.Now().Before(item.expiresAt) {
			return isChinaIP(item.addr)
		}
		dnsCache.Delete(targetHost)
	}

	ips, err := net.LookupIP(targetHost)
	if err == nil && len(ips) > 0 {
		if addr, ok := netip.AddrFromSlice(ips[0]); ok {
			dnsCache.Store(targetHost, dnsCacheItem{addr: addr, expiresAt: time.Now().Add(30 * time.Minute)})
			return isChinaIP(addr)
		}
	}
	return false
}

func isChinaIP(addr netip.Addr) bool {
	ipRangesMu.RLock()
	defer ipRangesMu.RUnlock()
	if addr.Is4() || addr.Is4In6() {
		ip4Bytes := addr.As4()
		val := binary.BigEndian.Uint32(ip4Bytes[:])
		l, r := 0, len(chinaIPv4Ranges)
		for l < r {
			mid := (l + r) / 2
			if val < chinaIPv4Ranges[mid].start {
				r = mid
			} else if val > chinaIPv4Ranges[mid].end {
				l = mid + 1
			} else {
				return true
			}
		}
		return false
	}
	if addr.Is6() {
		ip16Bytes := addr.As16()
		high := binary.BigEndian.Uint64(ip16Bytes[:8])
		low := binary.BigEndian.Uint64(ip16Bytes[8:])
		l, r := 0, len(chinaIPv6Ranges)
		for l < r {
			mid := (l + r) / 2
			midV := chinaIPv6Ranges[mid]
			if high < midV.startHigh || (high == midV.startHigh && low < midV.startLow) {
				r = mid
			} else if high > midV.endHigh || (high == midV.endHigh && low > midV.endLow) {
				l = mid + 1
			} else {
				return true
			}
		}
	}
	return false
}

func cleanDNSCacheLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		dnsCache.Range(func(key, value interface{}) bool {
			if now.After(value.(dnsCacheItem).expiresAt) {
				dnsCache.Delete(key)
			}
			return true
		})
	}
}

func loadRoutingRules() error {
	if _, err := os.Stat(dataPath("geosite_cn.txt")); os.IsNotExist(err) {
		return fetchAndReloadRules()
	}

	newTrie := &trieNode{children: make(map[string]*trieNode)}
	if f, err := os.Open(dataPath("geosite_cn.txt")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "full:")
			line = strings.TrimPrefix(line, "domain:")
			insertToTrie(newTrie, line)
		}
		f.Close()
	}

	var newIPv4Ranges []ipRange
	if f, err := os.Open(dataPath("geoip_v4.txt")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' {
				continue
			}
			if prefix, err := netip.ParsePrefix(line); err == nil && prefix.Addr().Is4() {
				startBytes := prefix.Addr().As4()
				startVal := binary.BigEndian.Uint32(startBytes[:])
				shift := 32 - prefix.Bits()
				var endVal uint32
				if shift == 32 {
					endVal = 0xFFFFFFFF
				} else {
					endVal = startVal | ((1 << shift) - 1)
				}
				newIPv4Ranges = append(newIPv4Ranges, ipRange{start: startVal, end: endVal})
			}
		}
		f.Close()
	}

	var newIPv6Ranges []ip6Range
	if f, err := os.Open(dataPath("geoip_v6.txt")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' {
				continue
			}
			if prefix, err := netip.ParsePrefix(line); err == nil && prefix.Addr().Is6() {
				startBytes := prefix.Addr().As16()
				endBytes := startBytes
				maskLen := prefix.Bits()
				for i := maskLen; i < 128; i++ {
					endBytes[i/8] |= (1 << (7 - (i % 8)))
				}
				newIPv6Ranges = append(newIPv6Ranges, ip6Range{
					startHigh: binary.BigEndian.Uint64(startBytes[:8]), startLow: binary.BigEndian.Uint64(startBytes[8:]),
					endHigh: binary.BigEndian.Uint64(endBytes[:8]), endLow: binary.BigEndian.Uint64(endBytes[8:]),
				})
			}
		}
		f.Close()
	}

	domainTrieMu.Lock()
	domainTrie = newTrie
	domainTrieMu.Unlock()

	ipRangesMu.Lock()
	chinaIPv4Ranges = newIPv4Ranges
	chinaIPv6Ranges = newIPv6Ranges
	ipRangesMu.Unlock()
	return nil
}

func updateRoutingRulesTask(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		log.Printf("[路由] 正在后台拉取最新规则...")
		if err := fetchAndReloadRules(); err == nil {
			log.Printf("[路由] 规则热重载完成！")
		}
	}
}

func fetchAndReloadRules() error {
	files := map[string]string{
		"geosite_cn.tmp": "https://raw.githubusercontent.com/Loyalsoldier/v2ray-rules-dat/release/direct-list.txt",
		"geoip_v4.tmp":   "https://raw.githubusercontent.com/misakaio/chnroutes2/master/chnroutes.txt",
		"geoip_v6.tmp":   "https://raw.githubusercontent.com/ChanthMiao/China-IPv6-List/release/cn6.txt",
	}
	for fileName, url := range files {
		downloadFile(url, dataPath(fileName))
	}

	newTrie := &trieNode{children: make(map[string]*trieNode)}
	if f, err := os.Open(dataPath("geosite_cn.tmp")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "full:")
			line = strings.TrimPrefix(line, "domain:")
			insertToTrie(newTrie, line)
		}
		f.Close()
	}

	var newIPv4Ranges []ipRange
	if f, err := os.Open(dataPath("geoip_v4.tmp")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' {
				continue
			}
			if prefix, err := netip.ParsePrefix(line); err == nil && prefix.Addr().Is4() {
				startBytes := prefix.Addr().As4()
				startVal := binary.BigEndian.Uint32(startBytes[:])
				shift := 32 - prefix.Bits()
				var endVal uint32
				if shift == 32 {
					endVal = 0xFFFFFFFF
				} else {
					endVal = startVal | ((1 << shift) - 1)
				}
				newIPv4Ranges = append(newIPv4Ranges, ipRange{start: startVal, end: endVal})
			}
		}
		f.Close()
	}

	var newIPv6Ranges []ip6Range
	if f, err := os.Open(dataPath("geoip_v6.tmp")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' {
				continue
			}
			if prefix, err := netip.ParsePrefix(line); err == nil && prefix.Addr().Is6() {
				startBytes := prefix.Addr().As16()
				endBytes := startBytes
				maskLen := prefix.Bits()
				for i := maskLen; i < 128; i++ {
					endBytes[i/8] |= (1 << (7 - (i % 8)))
				}
				newIPv6Ranges = append(newIPv6Ranges, ip6Range{
					startHigh: binary.BigEndian.Uint64(startBytes[:8]), startLow: binary.BigEndian.Uint64(startBytes[8:]),
					endHigh: binary.BigEndian.Uint64(endBytes[:8]), endLow: binary.BigEndian.Uint64(endBytes[8:]),
				})
			}
		}
		f.Close()
	}

	domainTrieMu.Lock()
	domainTrie = newTrie
	domainTrieMu.Unlock()

	ipRangesMu.Lock()
	if len(newIPv4Ranges) > 0 {
		chinaIPv4Ranges = newIPv4Ranges
	}
	if len(newIPv6Ranges) > 0 {
		chinaIPv6Ranges = newIPv6Ranges
	}
	ipRangesMu.Unlock()

	os.Rename(dataPath("geosite_cn.tmp"), dataPath("geosite_cn.txt"))
	os.Rename(dataPath("geoip_v4.tmp"), dataPath("geoip_v4.txt"))
	os.Rename(dataPath("geoip_v6.tmp"), dataPath("geoip_v6.txt"))

	return nil
}

func downloadFile(url, path string) {
	os.Remove(path)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	out, err := os.Create(path)
	if err != nil {
		return
	}
	defer out.Close()
	io.Copy(out, resp.Body)
}

// ======================== gomobile 导出接口 (供 Java/JNI 调用) ========================

// SetDataDirectory 设置规则与缓存文件使用的应用私有目录。
func SetDataDirectory(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("数据目录不能为空")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("解析数据目录: %w", err)
	}
	if err := os.MkdirAll(absDir, 0700); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	dataDirMu.Lock()
	dataDir = absDir
	dataDirMu.Unlock()
	return nil
}

func dataPath(name string) string {
	dataDirMu.RLock()
	dir := dataDir
	dataDirMu.RUnlock()
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

var (
	// 全局代理服务器状态
	proxyServerRunning  bool
	proxyServerCancel   context.CancelFunc
	proxyServerListener net.Listener
	proxyServerMu       sync.Mutex
)

// StartSocksProxy 启动本地 SOCKS5 代理并桥接到远端 WSS/ECH
// 对应 Java: Tunnel.startSocksProxy(localAddr, wsAddr, echDns, echDomain, prefIp, token)
func StartSocksProxy(localAddr, wsAddr, echDns, echName, prefIp, tokenStr string) error {
	return StartSocksProxyWithOptions(localAddr, wsAddr, echDns, echName, prefIp, tokenStr, "bypass_cn", false, true)
}

func StartSocksProxyWithOptions(localAddr, wsAddr, echDns, echName, prefIp, tokenStr, routeMode string, fakeIP, autoBest bool) error {
	localAddr = strings.TrimSpace(localAddr)
	if _, err := net.ResolveTCPAddr("tcp", localAddr); err != nil {
		return fmt.Errorf("无效的本地监听地址: %w", err)
	}
	if _, _, _, err := parseServerAddr(wsAddr); err != nil {
		return err
	}
	normalizedDNS, err := normalizeDoHURL(echDns)
	if err != nil {
		return err
	}
	echName = strings.TrimSpace(echName)
	if echName == "" {
		return errors.New("ECH 域名不能为空")
	}

	proxyServerMu.Lock()
	defer proxyServerMu.Unlock()
	if proxyServerRunning {
		return errors.New("代理服务已在运行")
	}

	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", localAddr, err)
	}

	serverAddr = strings.TrimSpace(wsAddr)
	dnsServer = normalizedDNS
	echDomain = echName
	token = tokenStr
	serverIP = strings.TrimSpace(prefIp)
	routingMode = normalizeRoutingMode(routeMode)
	useFakeIP = fakeIP
	listenAddr = localAddr
	echListMu.Lock()
	echList = nil
	echListMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	proxyServerCancel = cancel
	proxyServerListener = listener
	proxyServerRunning = true

	go func() {
		log.Printf("[config] routing=%s fake_ip=%t auto_best=%t preferred_ips=%s", routingMode, useFakeIP, autoBest, serverIP)
		_ = prepareECH()
		go startECHAutoRefresh(ctx)
		go cleanDNSCacheLoop(ctx)
		preferredIPs := serverIP
		if autoBest {
			preferredIPs = strings.Join(rankPreferredIPs(ctx, serverIP, 3), ",")
		}
		initNodeManager(ctx, preferredIPs)
		go startWSPoolManager(ctx)

		if routingMode == "bypass_cn" {
			if err := loadRoutingRules(); err != nil {
				log.Printf("[警告] 路由加载失败: %v", err)
			} else {
				log.Printf("[启动] 路由加载完毕 (IPv4: %d, IPv6: %d)", len(chinaIPv4Ranges), len(chinaIPv6Ranges))
				go updateRoutingRulesTask(ctx)
			}
		}

		if useFakeIP {
			go startFakeIPServer(ctx)
		}
	}()

	go runProxyServerWithContext(ctx, listener)
	return nil
}

// StopSocksProxy 停止本地 SOCKS5 代理
// 对应 Java: Tunnel.stopSocksProxy()
func normalizeRoutingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "global", "none", "bypass_cn":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "bypass_cn"
	}
}

func StopSocksProxy() {
	proxyServerMu.Lock()
	if !proxyServerRunning {
		proxyServerMu.Unlock()
		return
	}
	cancel := proxyServerCancel
	listener := proxyServerListener
	proxyServerCancel = nil
	proxyServerListener = nil
	proxyServerRunning = false
	proxyServerMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}

	for len(wsPool) > 0 {
		select {
		case item := <-wsPool:
			_ = item.conn.Close()
		default:
		}
	}

	nodeMu.Lock()
	nodePool = nil
	nodeMu.Unlock()

	dnsCache.Range(func(key, value interface{}) bool {
		dnsCache.Delete(key)
		return true
	})

	log.Printf("[代理] 服务已停止")
}

// runProxyServerWithContext 带 context 控制的代理服务器
func runProxyServerWithContext(ctx context.Context, listener net.Listener) {
	defer listener.Close()
	log.Printf("[代理] 服务器就绪: %s", listener.Addr())

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("[代理] 收到停止信号，正在关闭...")
				return
			}
			log.Printf("[代理] Accept 错误: %v", err)
			return
		}
		go handleConnection(conn)
	}
}
