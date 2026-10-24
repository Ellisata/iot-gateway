// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

const (
	defaultListenHost   = "0.0.0.0"
	defaultListenPort   = 4840
	defaultNamespaceURI = "urn:iot-gateway:devices"
	defaultRootName     = "iot-gateway"

	securityNone = "None"

	nodeIDStyleID   = "id"
	nodeIDStyleName = "name"
)

// 支持的 OPC UA 安全策略。与 gopcua/uapolicy 的注册表对齐：
// 只列这里有的名字，避免 EnableSecurity 静默丢弃端点（库内部只打一行 log）。
var supportedSecurityPolicies = map[string]struct{}{
	"None":                  {},
	"Basic128Rsa15":         {},
	"Basic256":              {},
	"Basic256Sha256":        {},
	"Aes128_Sha256_RsaOaep": {},
	"Aes256_Sha256_RsaPss":  {},
}

// config 解析自 push_channel.config_json。
//
// 默认值面向「内网、匿名、不加密」的最小可用配置；需要加密时填
// securityPolicy/securityMode 并给出 PEM 证书与私钥路径。
type config struct {
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`

	NamespaceURI string `json:"namespaceUri"`
	RootName     string `json:"rootName"`

	SecurityPolicy string `json:"securityPolicy"`
	SecurityMode   string `json:"securityMode"`
	CertFile       string `json:"certFile"`
	KeyFile        string `json:"keyFile"`

	// NodeIDStyle 决定节点标识符：id（默认，稳定）或 name（可读，但改名后失效）。
	NodeIDStyle string `json:"nodeIdStyle"`

	// DeviceIDs 只暴露这些设备；空表示全部启用设备。
	DeviceIDs stringList `json:"deviceIds"`

	// AdvertiseHosts 额外对外公布的地址（DNS 别名、NAT 公网 IP 等）。
	// 只登记为端点描述，不绑定监听；监听地址始终是 ListenHost。
	AdvertiseHosts stringList `json:"advertiseHosts"`
}

// stringList 同时接受 JSON 数组与逗号分隔字符串。
//
// 管理端动态表单的 input 只能产出字符串，而 API 调用方更愿意传数组；
// 两种写法都收，避免为了一个字段在表单里引入多选组件。
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*l = normalizeList(arr)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("opcua: deviceIds 仅支持数组或逗号分隔字符串")
	}
	*l = normalizeList(strings.Split(s, ","))
	return nil
}

func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func parseConfig(raw string) (*config, error) {
	cfg := &config{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			return nil, fmt.Errorf("opcua: 配置解析失败: %w", err)
		}
	}

	cfg.ListenHost = strings.TrimSpace(cfg.ListenHost)
	if cfg.ListenHost == "" {
		cfg.ListenHost = defaultListenHost
	}
	if strings.ContainsAny(cfg.ListenHost, "\r\n/ ") {
		return nil, fmt.Errorf("opcua: listenHost 非法: %q", cfg.ListenHost)
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = defaultListenPort
	}
	if cfg.ListenPort < 1 || cfg.ListenPort > 65535 {
		return nil, fmt.Errorf("opcua: listenPort 必须在 1-65535 之间")
	}

	cfg.NamespaceURI = strings.TrimSpace(cfg.NamespaceURI)
	if cfg.NamespaceURI == "" {
		cfg.NamespaceURI = defaultNamespaceURI
	}
	cfg.RootName = strings.TrimSpace(cfg.RootName)
	if cfg.RootName == "" {
		cfg.RootName = defaultRootName
	}

	cfg.SecurityPolicy = strings.TrimSpace(cfg.SecurityPolicy)
	if cfg.SecurityPolicy == "" {
		cfg.SecurityPolicy = securityNone
	}
	if _, ok := supportedSecurityPolicies[cfg.SecurityPolicy]; !ok {
		return nil, fmt.Errorf("opcua: 不支持的安全策略 %q", cfg.SecurityPolicy)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.SecurityMode)) {
	case "", "none":
		cfg.SecurityMode = "None"
	case "sign":
		cfg.SecurityMode = "Sign"
	case "signandencrypt":
		cfg.SecurityMode = "SignAndEncrypt"
	default:
		return nil, fmt.Errorf("opcua: 不支持的安全模式 %q", cfg.SecurityMode)
	}
	if cfg.SecurityPolicy == securityNone && cfg.SecurityMode != "None" {
		return nil, fmt.Errorf("opcua: securityPolicy=None 时 securityMode 必须为 None")
	}
	if cfg.SecurityPolicy != securityNone && cfg.SecurityMode == "None" {
		return nil, fmt.Errorf("opcua: securityPolicy 非 None 时 securityMode 不能为 None")
	}
	if cfg.SecurityPolicy != securityNone {
		cfg.CertFile = strings.TrimSpace(cfg.CertFile)
		cfg.KeyFile = strings.TrimSpace(cfg.KeyFile)
		if cfg.CertFile == "" || cfg.KeyFile == "" {
			return nil, fmt.Errorf("opcua: 启用安全策略时必须填写 certFile 与 keyFile")
		}
		if _, err := os.Stat(cfg.CertFile); err != nil {
			return nil, fmt.Errorf("opcua: 证书文件不可读: %w", err)
		}
		if _, err := os.Stat(cfg.KeyFile); err != nil {
			return nil, fmt.Errorf("opcua: 私钥文件不可读: %w", err)
		}
	}

	switch strings.ToLower(strings.TrimSpace(cfg.NodeIDStyle)) {
	case "", nodeIDStyleID:
		cfg.NodeIDStyle = nodeIDStyleID
	case nodeIDStyleName:
		cfg.NodeIDStyle = nodeIDStyleName
	default:
		return nil, fmt.Errorf("opcua: nodeIdStyle 仅支持 id 或 name")
	}

	cfg.DeviceIDs = normalizeList(cfg.DeviceIDs)

	cfg.AdvertiseHosts = normalizeList(cfg.AdvertiseHosts)
	for _, host := range cfg.AdvertiseHosts {
		// server.EndPoint 只接受裸主机名/IPv4（IPv6 的方括号由它自己拼不出来）。
		if strings.Contains(host, "://") || strings.ContainsAny(host, "/: \r\n") {
			return nil, fmt.Errorf("opcua: advertiseHosts 非法 %q（只填主机名或 IPv4，不含协议/端口）", host)
		}
	}
	return cfg, nil
}

func (c *config) addr() string {
	return net.JoinHostPort(c.ListenHost, fmt.Sprintf("%d", c.ListenPort))
}

func (c *config) endpoint() string {
	return "opc.tcp://" + c.addr()
}

func (c *config) deviceSet() map[string]struct{} {
	if len(c.DeviceIDs) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(c.DeviceIDs))
	for _, id := range c.DeviceIDs {
		out[id] = struct{}{}
	}
	return out
}
