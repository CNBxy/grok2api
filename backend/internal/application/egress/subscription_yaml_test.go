package egress

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseYAMLProxySubscriptionImportsSupportedNodes(t *testing.T) {
	subscription := `
proxies:
  - name: "🇭🇰 香港 01"
    type: trojan
    server: hk.example.com
    port: 443
    password: secret
    sni: hk.example.com
    network: ws
    ws-opts:
      path: /trojan
      headers:
        Host: hk.example.com
    skip-cert-verify: true
  - name: 日本 02
    type: vless
    server: jp.example.com
    port: 8443
    uuid: 123e4567-e89b-12d3-a456-426614174000
    tls: true
    servername: jp.example.com
  - name: 新加坡 03
    type: ss
    server: sg.example.com
    port: 8388
    cipher: aes-256-gcm
    password: sg-pass
  - name: 美国 04
    type: vmess
    server: us.example.com
    port: 443
    uuid: 123e4567-e89b-12d3-a456-426614174000
    alterId: 0
    cipher: auto
    tls: true
    network: ws
    ws-opts:
      path: /vmess
      headers:
        Host: us.example.com
  - name: 台湾 05
    type: http
    server: tw.example.com
    port: 8080
    username: alice
    password: bob
    tls: true
  - name: 韩国 06
    type: socks5
    server: kr.example.com
    port: 1080
`
	entries, skipped, err := parseYAMLProxySubscription(subscription)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 || skipped != 0 {
		t.Fatalf("entries=%d skipped=%d", len(entries), skipped)
	}
	expectedNames := []string{"🇭🇰 香港 01", "日本 02", "新加坡 03", "美国 04", "台湾 05", "韩国 06"}
	for index, entry := range entries {
		if entry.Name != expectedNames[index] {
			t.Fatalf("entry %d name=%q want %q", index, entry.Name, expectedNames[index])
		}
		if len(entry.Key) != 64 {
			t.Fatalf("entry %d key=%q", index, entry.Key)
		}
		if _, err := NormalizeProxyURL(entry.ProxyURL); err != nil {
			t.Fatalf("entry %d URL %q rejected: %v", index, entry.ProxyURL, err)
		}
	}
	trojanURL := entries[0].ProxyURL
	for _, fragment := range []string{"security=tls", "type=ws", "allowInsecure=true", "path=%2Ftrojan", "host=hk.example.com"} {
		if !strings.Contains(trojanURL, fragment) {
			t.Fatalf("trojan URL %q missing %q", trojanURL, fragment)
		}
	}
	if !strings.HasPrefix(entries[3].ProxyURL, "vmess://") {
		t.Fatalf("vmess URL malformed: %q", entries[3].ProxyURL)
	}
	if !strings.HasPrefix(entries[4].ProxyURL, "https://") {
		t.Fatalf("tls http node should map to https: %q", entries[4].ProxyURL)
	}
}

func TestParseYAMLProxySubscriptionSkipsUnsupportedNodes(t *testing.T) {
	subscription := `
proxies:
  - name: valid
    type: trojan
    server: ok.example.com
    port: 443
    password: secret
  - name: hysteria2
    type: hysteria2
    server: hy.example.com
    port: 443
  - name: tuic
    type: tuic
    server: tu.example.com
    port: 443
  - name: flow-vless
    type: vless
    server: fl.example.com
    port: 443
    uuid: 123e4567-e89b-12d3-a456-426614174000
    flow: xtls-rprx-vision
  - name: plugin-ss
    type: ss
    server: ss.example.com
    port: 8388
    cipher: chacha20-ietf-poly1305
    password: secret
    plugin: obfs-local
  - name: grpc-vmess
    type: vmess
    server: vm.example.com
    port: 443
    uuid: 123e4567-e89b-12d3-a456-426614174000
    network: grpc
  - name: missing-server
    type: trojan
    port: 443
    password: secret
`
	entries, skipped, err := parseYAMLProxySubscription(subscription)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || skipped != 6 {
		t.Fatalf("entries=%d skipped=%d", len(entries), skipped)
	}
	if entries[0].Name != "valid" {
		t.Fatalf("name=%q", entries[0].Name)
	}
}

func TestParseProxySubscriptionAcceptsYAML(t *testing.T) {
	subscription := "proxies:\n  - name: 节点A\n    type: trojan\n    server: node.example.com\n    port: 443\n    password: secret\n"
	entries, skipped, err := parseProxySubscription(subscription)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || skipped != 0 || entries[0].Name != "节点A" {
		t.Fatalf("entries=%#v skipped=%d", entries, skipped)
	}
}

func TestParseProxySubscriptionRejectsYAMLWithOnlyUnsupportedNodes(t *testing.T) {
	subscription := "proxies:\n  - name: hy\n    type: hysteria2\n    server: hy.example.com\n    port: 443\n"
	if _, _, err := parseProxySubscription(subscription); err == nil {
		t.Fatal("YAML subscription with only unsupported nodes was accepted")
	}
}

func TestParseYAMLProxySubscriptionDeduplicatesNodes(t *testing.T) {
	subscription := `
proxies:
  - name: 重复1
    type: trojan
    server: node.example.com
    port: 443
    password: secret
  - name: 重复2
    type: trojan
    server: node.example.com
    port: 443
    password: secret
`
	entries, skipped, err := parseYAMLProxySubscription(subscription)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || skipped != 1 || entries[0].Name != "重复1" {
		t.Fatalf("entries=%#v skipped=%d", entries, skipped)
	}
}

func TestYAMLNodeNameSanitization(t *testing.T) {
	subscription := fmt.Sprintf("proxies:\n  - name: \"%s\\x01\\x02\"\n    type: trojan\n    server: node.example.com\n    port: 443\n    password: secret\n", strings.Repeat("长", 200))
	entries, _, err := parseYAMLProxySubscription(subscription)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries=%d", len(entries))
	}
	if utf8.RuneCountInString(entries[0].Name) != 160 {
		t.Fatalf("name length=%d want 160", utf8.RuneCountInString(entries[0].Name))
	}
	if strings.ContainsAny(entries[0].Name, "\x01\x02") {
		t.Fatalf("name retained control characters: %q", entries[0].Name)
	}
}

func TestParseYAMLProxySubscriptionRejectsMissingProxies(t *testing.T) {
	if _, _, err := parseYAMLProxySubscription("rules:\n  - DOMAIN,example.com,PROXY\n"); err == nil {
		t.Fatal("YAML without proxies list was accepted")
	}
}

func TestSubscriptionNodeNamePrefersStructuredName(t *testing.T) {
	entry := subscriptionEntry{Name: "机场节点", ProxyURL: "http://node.example.com:8080"}
	if name := subscriptionNodeName(entry, "订阅源", 0); name != "机场节点" {
		t.Fatalf("name=%q", name)
	}
	if name := subscriptionNodeName(subscriptionEntry{}, "订阅源", 0); name != "订阅源 001" {
		t.Fatalf("fallback name=%q", name)
	}
}
