package tunnel

import "testing"

func TestIsLikelyChinaDomain(t *testing.T) {
	for _, domain := range []string{"baidu.com", "www.bilibili.com", "service.example.cn"} {
		if !isLikelyChinaDomain(domain) {
			t.Fatalf("expected domestic domain %q", domain)
		}
	}
	if isLikelyChinaDomain("example.com") {
		t.Fatal("did not expect example.com to be classified as domestic")
	}
}

func TestParsePreferredIPs(t *testing.T) {
	got := parsePreferredIPs("1.1.1.1\n2.2.2.2 # backup, 1.1.1.1")
	if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "2.2.2.2" {
		t.Fatalf("unexpected preferred IPs: %#v", got)
	}
}
