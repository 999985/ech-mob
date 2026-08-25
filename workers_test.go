package tunnel

import (
	"path/filepath"
	"testing"
)

func TestParseServerAddr(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		host    string
		port    string
		path    string
		wantErr bool
	}{
		{name: "bare host", input: "worker.example.com", host: "worker.example.com", port: "443", path: "/"},
		{name: "wss URL", input: "wss://worker.example.com/ws?ed=2560", host: "worker.example.com", port: "443", path: "/ws?ed=2560"},
		{name: "custom port", input: "wss://worker.example.com:8443/tunnel", host: "worker.example.com", port: "8443", path: "/tunnel"},
		{name: "invalid scheme", input: "ws://worker.example.com", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, port, path, err := parseServerAddr(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseServerAddr(%q) returned no error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseServerAddr(%q): %v", test.input, err)
			}
			if host != test.host || port != test.port || path != test.path {
				t.Fatalf("parseServerAddr(%q) = (%q, %q, %q), want (%q, %q, %q)", test.input, host, port, path, test.host, test.port, test.path)
			}
		})
	}
}

func TestNormalizeDoHURL(t *testing.T) {
	tests := map[string]string{
		"dns.alidns.com":            "https://dns.alidns.com/dns-query",
		"dns.alidns.com/dns-query":  "https://dns.alidns.com/dns-query",
		"https://doh.pub/dns-query": "https://doh.pub/dns-query",
	}

	for input, want := range tests {
		got, err := normalizeDoHURL(input)
		if err != nil {
			t.Fatalf("normalizeDoHURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeDoHURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSetDataDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := SetDataDirectory(dir); err != nil {
		t.Fatalf("SetDataDirectory: %v", err)
	}
	want := filepath.Join(dir, "cache.json")
	if got := dataPath("cache.json"); got != want {
		t.Fatalf("dataPath = %q, want %q", got, want)
	}
}
