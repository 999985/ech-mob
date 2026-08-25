import sys

with open("workers.go", "r", encoding="utf-8") as f:
    content = f.read()

if "context" not in content:
    import_start = content.find("import (")
    if import_start != -1:
        import_end = content.find(")", import_start)
        if import_end != -1:
            imports = content[import_start:import_end]
            if "context" not in imports:
                new_imports = imports + "\n\t\"context\""
                content = content[:import_start] + new_imports + content[import_end:]

new_exports = """

// ======================== gomobile 导出接口 (供 Java/JNI 调用) ========================

var (
    // 全局代理服务器状态
    proxyServerRunning bool
    proxyServerCancel  context.CancelFunc
    proxyServerMu      sync.Mutex
)

// StartSocksProxy 启动本地 SOCKS5 代理并桥接到远端 WSS/ECH
// 对应 Java: Tunnel.startSocksProxy(localAddr, wsAddr, echDns, echDomain, prefIp, token)
func StartSocksProxy(localAddr, wsAddr, echDns, echDomain, prefIp, tokenStr string) error {
    proxyServerMu.Lock()
    defer proxyServerMu.Unlock()

    if proxyServerRunning {
        return errors.New("代理服务已在运行")
    }

    serverAddr = wsAddr
    dnsServer = "https://" + echDns + "/dns-query"
    echDomain = echDomain
    token = tokenStr
    if prefIp != "" {
        serverIP = prefIp
    }
    listenAddr = localAddr

    ctx, cancel := context.WithCancel(context.Background())
    proxyServerCancel = cancel

    go func() {
        _ = prepareECH()
        go startECHAutoRefresh()
        go cleanDNSCacheLoop()

        if routingMode == "bypass_cn" {
            if err := loadRoutingRules(); err != nil {
                log.Printf("[警告] 路由加载失败: %v", err)
            } else {
                log.Printf("[启动] 路由加载完毕 (IPv4: %d, IPv6: %d)", len(chinaIPv4Ranges), len(chinaIPv6Ranges))
                go updateRoutingRulesTask()
            }
        }

        initNodeManager(serverIP)
        go startWSPoolManager()

        if useFakeIP {
            go startFakeIPServer()
        }

        runProxyServerWithContext(ctx, listenAddr)
    }()

    proxyServerRunning = true
    return nil
}

// StopSocksProxy 停止本地 SOCKS5 代理
// 对应 Java: Tunnel.stopSocksProxy()
func StopSocksProxy() {
    proxyServerMu.Lock()
    defer proxyServerMu.Unlock()

    if !proxyServerRunning {
        return
    }

    if proxyServerCancel != nil {
        proxyServerCancel()
        proxyServerCancel = nil
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

    proxyServerRunning = false
    log.Printf("[代理] 服务已停止")
}

// runProxyServerWithContext 带 context 控制的代理服务器
func runProxyServerWithContext(ctx context.Context, addr string) {
    listener, err := net.Listen("tcp", addr)
    if err != nil {
        log.Printf("[代理] 监听失败: %v", err)
        return
    }
    defer listener.Close()

    log.Printf("[代理] 服务器就绪: %s", addr)

    connChan := make(chan net.Conn, 1)
    errChan := make(chan error, 1)

    go func() {
        for {
            conn, err := listener.Accept()
            if err != nil {
                select {
                case errChan <- err:
                case <-ctx.Done():
                }
                return
            }
            select {
            case connChan <- conn:
            case <-ctx.Done():
                conn.Close()
                return
            }
        }
    }()

    for {
        select {
        case <-ctx.Done():
            log.Printf("[代理] 收到停止信号，正在关闭...")
            return
        case err := <-errChan:
            if !strings.Contains(err.Error(), "use of closed network connection") {
                log.Printf("[代理] Accept 错误: %v", err)
            }
            return
        case conn := <-connChan:
            go handleConnection(conn)
        }
    }
}
"""

insert_marker = "func downloadFile(url, path string) {"
idx = content.rfind(insert_marker)
if idx == -1:
    print("ERROR: Could not find downloadFile function")
    sys.exit(1)

next_func = content.find("\nfunc ", idx + 1)
if next_func == -1:
    new_content = content + new_exports
else:
    new_content = content[:next_func] + new_exports + content[next_func:]

with open("workers.go", "w", encoding="utf-8") as f:
    f.write(new_content)

print("Successfully updated workers.go")
