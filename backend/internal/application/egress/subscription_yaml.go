package egress

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// parseYAMLProxySubscription converts a mihomo/Clash style YAML subscription
// (the top-level `proxies:` node list) into normalized subscription entries.
// Unsupported protocols, unsupported transport variants, malformed nodes, and
// duplicates are counted as skipped so a partial YAML list still imports.
func parseYAMLProxySubscription(value string) ([]subscriptionEntry, int, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(value, "\ufeff")), &document); err != nil {
		return nil, 0, err
	}
	rawProxies, ok := document["proxies"]
	if !ok {
		return nil, 0, errors.New("YAML 订阅缺少 proxies 列表")
	}
	proxies, ok := rawProxies.([]any)
	if !ok || len(proxies) == 0 {
		return nil, 0, errors.New("YAML 订阅 proxies 必须是节点列表")
	}
	seen := make(map[string]struct{})
	entries := make([]subscriptionEntry, 0, len(proxies))
	skipped := 0
	for _, raw := range proxies {
		node, ok := raw.(map[string]any)
		if !ok {
			skipped++
			continue
		}
		normalized, err := normalizeYAMLProxyNode(node)
		if err != nil {
			skipped++
			continue
		}
		digest := sha256.Sum256([]byte(normalized))
		key := hex.EncodeToString(digest[:])
		if _, exists := seen[key]; exists {
			skipped++
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, subscriptionEntry{ProxyURL: normalized, Key: key, Name: yamlNodeName(node)})
		if len(entries) > maxSubscriptionEntries {
			return nil, skipped, nil
		}
	}
	if len(entries) == 0 {
		return nil, skipped, errors.New("YAML 订阅中没有可用的代理节点")
	}
	return entries, skipped, nil
}

func normalizeYAMLProxyNode(node map[string]any) (string, error) {
	switch strings.ToLower(strings.TrimSpace(yamlValueString(node, "type"))) {
	case "trojan":
		return yamlTrojanURL(node)
	case "vless":
		return yamlVLESSURL(node)
	case "ss":
		return yamlShadowsocksURL(node)
	case "vmess":
		return yamlVMessURL(node)
	case "http":
		return yamlUserInfoURL(node, "http")
	case "socks5":
		return yamlUserInfoURL(node, "socks5")
	default:
		return "", errors.New("不支持的节点类型")
	}
}

func yamlTrojanURL(node map[string]any) (string, error) {
	server, port, err := yamlServerPort(node)
	if err != nil {
		return "", err
	}
	password := yamlValueString(node, "password")
	if password == "" {
		return "", errors.New("Trojan 节点缺少 password")
	}
	query := url.Values{}
	query.Set("security", "tls")
	if sni := yamlValueString(node, "sni"); sni != "" {
		query.Set("sni", sni)
	}
	if _, err := yamlTunnelTransport(node, query); err != nil {
		return "", err
	}
	if yamlValueBool(node, "skip-cert-verify") {
		query.Set("allowInsecure", "true")
	}
	if alpn := yamlALPN(node); alpn != "" {
		query.Set("alpn", alpn)
	}
	return (&url.URL{Scheme: "trojan", User: url.User(password), Host: yamlJoinHostPort(server, port), RawQuery: query.Encode()}).String(), nil
}

func yamlVLESSURL(node map[string]any) (string, error) {
	server, port, err := yamlServerPort(node)
	if err != nil {
		return "", err
	}
	userID := yamlValueString(node, "uuid")
	if userID == "" {
		return "", errors.New("VLESS 节点缺少 uuid")
	}
	if flow := yamlValueString(node, "flow"); flow != "" {
		return "", errors.New("暂不支持 VLESS flow")
	}
	query := url.Values{}
	query.Set("encryption", "none")
	if yamlValueBool(node, "tls") {
		query.Set("security", "tls")
		if sni := yamlValueString(node, "servername"); sni != "" {
			query.Set("sni", sni)
		}
	} else {
		query.Set("security", "none")
	}
	if _, err := yamlTunnelTransport(node, query); err != nil {
		return "", err
	}
	if yamlValueBool(node, "skip-cert-verify") {
		query.Set("allowInsecure", "true")
	}
	if alpn := yamlALPN(node); alpn != "" {
		query.Set("alpn", alpn)
	}
	return (&url.URL{Scheme: "vless", User: url.User(userID), Host: yamlJoinHostPort(server, port), RawQuery: query.Encode()}).String(), nil
}

func yamlShadowsocksURL(node map[string]any) (string, error) {
	server, port, err := yamlServerPort(node)
	if err != nil {
		return "", err
	}
	password := yamlValueString(node, "password")
	if password == "" {
		return "", errors.New("Shadowsocks 节点缺少 password")
	}
	if plugin := yamlValueString(node, "plugin"); plugin != "" {
		return "", errors.New("暂不支持 Shadowsocks plugin")
	}
	method := strings.ToLower(yamlValueString(node, "cipher"))
	switch method {
	case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305":
	default:
		return "", fmt.Errorf("暂不支持 Shadowsocks cipher %q", method)
	}
	credential := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	return "ss://" + credential + "@" + yamlJoinHostPort(server, port), nil
}

func yamlVMessURL(node map[string]any) (string, error) {
	server, port, err := yamlServerPort(node)
	if err != nil {
		return "", err
	}
	userID := yamlValueString(node, "uuid")
	if userID == "" {
		return "", errors.New("VMess 节点缺少 uuid")
	}
	alterID := 0
	if raw, ok := yamlValueInt(node, "alterId"); ok {
		if raw < 0 || raw > 65535 {
			return "", errors.New("VMess alterId 无效")
		}
		alterID = raw
	}
	cipher := strings.ToLower(yamlValueString(node, "cipher"))
	if cipher == "" {
		cipher = "auto"
	}
	switch cipher {
	case "auto", "aes-128-gcm", "chacha20-poly1305", "none":
	default:
		return "", fmt.Errorf("暂不支持 VMess cipher %q", cipher)
	}
	transport := "tcp"
	path, host := "", ""
	switch strings.ToLower(yamlValueString(node, "network")) {
	case "", "tcp", "none":
	case "ws", "websocket":
		transport = "ws"
		wsOpts := yamlValueMap(node, "ws-opts")
		path = yamlValueString(wsOpts, "path")
		headers := yamlValueMap(wsOpts, "headers")
		host = yamlFirstNonEmpty(yamlValueString(headers, "Host"), yamlValueString(headers, "host"))
	default:
		return "", fmt.Errorf("暂不支持 VMess %s 传输", yamlValueString(node, "network"))
	}
	share := map[string]any{
		"v": "2", "add": server, "port": strconv.Itoa(port), "id": userID,
		"aid": strconv.Itoa(alterID), "scy": cipher, "net": transport,
	}
	if yamlValueBool(node, "tls") {
		share["tls"] = "tls"
	}
	if sni := yamlValueString(node, "servername"); sni != "" {
		share["sni"] = sni
	}
	if transport == "ws" {
		if host != "" {
			share["host"] = host
		}
		if path != "" {
			share["path"] = path
		}
	}
	if yamlValueBool(node, "skip-cert-verify") {
		share["allowInsecure"] = true
	}
	encoded, err := json.Marshal(share)
	if err != nil {
		return "", err
	}
	return "vmess://" + base64.RawStdEncoding.EncodeToString(encoded), nil
}

func yamlUserInfoURL(node map[string]any, scheme string) (string, error) {
	server, port, err := yamlServerPort(node)
	if err != nil {
		return "", err
	}
	if scheme == "http" && yamlValueBool(node, "tls") {
		scheme = "https"
	}
	value := &url.URL{Scheme: scheme, Host: yamlJoinHostPort(server, port)}
	if username := yamlValueString(node, "username"); username != "" {
		if password := yamlValueString(node, "password"); password != "" {
			value.User = url.UserPassword(username, password)
		} else {
			value.User = url.User(username)
		}
	}
	return value.String(), nil
}

// yamlTunnelTransport maps the Clash `network`/`ws-opts` fields onto tunnel
// proxy query parameters. Only TCP and WebSocket transports are supported.
func yamlTunnelTransport(node map[string]any, query url.Values) (string, error) {
	switch strings.ToLower(yamlValueString(node, "network")) {
	case "", "tcp", "none":
		return "tcp", nil
	case "ws", "websocket":
		query.Set("type", "ws")
		wsOpts := yamlValueMap(node, "ws-opts")
		if path := yamlValueString(wsOpts, "path"); path != "" {
			query.Set("path", path)
		}
		headers := yamlValueMap(wsOpts, "headers")
		if host := yamlFirstNonEmpty(yamlValueString(headers, "Host"), yamlValueString(headers, "host")); host != "" {
			query.Set("host", host)
		}
		return "ws", nil
	default:
		return "", fmt.Errorf("暂不支持 %s 传输", yamlValueString(node, "network"))
	}
}

func yamlServerPort(node map[string]any) (string, int, error) {
	server := yamlValueString(node, "server")
	if server == "" {
		return "", 0, errors.New("节点缺少 server")
	}
	port, ok := yamlValueInt(node, "port")
	if !ok || port < 1 || port > 65535 {
		return "", 0, errors.New("节点端口无效")
	}
	return server, port, nil
}

func yamlJoinHostPort(server string, port int) string {
	return net.JoinHostPort(server, strconv.Itoa(port))
}

func yamlFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func yamlNodeName(node map[string]any) string {
	name := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, yamlValueString(node, "name"))
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 160 {
		name = string([]rune(name)[:160])
	}
	return name
}

func yamlValueString(node map[string]any, key string) string {
	raw, ok := node[key]
	if !ok || raw == nil {
		return ""
	}
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func yamlValueBool(node map[string]any, key string) bool {
	raw, ok := node[key]
	if !ok || raw == nil {
		return false
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func yamlValueInt(node map[string]any, key string) (int, bool) {
	raw, ok := node[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch typed := raw.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float64:
		if typed == float64(int(typed)) {
			return int(typed), true
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func yamlValueMap(node map[string]any, key string) map[string]any {
	raw, ok := node[key]
	if !ok {
		return nil
	}
	if typed, ok := raw.(map[string]any); ok {
		return typed
	}
	return nil
}

func yamlALPN(node map[string]any) string {
	raw, ok := node["alpn"]
	if !ok {
		return ""
	}
	switch typed := raw.(type) {
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				parts = append(parts, strings.TrimSpace(value))
			}
		}
		return strings.Join(parts, ",")
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}
