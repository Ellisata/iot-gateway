package opcua

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gopcua/opcua"
)

// OpcUaConfig OPC UA 协议驱动配置。
//
// protocol_json 支持两种端点表达方式（任选其一）：
//   - endpoint：完整端点地址，如 "opc.tcp://192.168.1.100:4840"（优先）；
//   - host + port：省略 endpoint 时自动合成 opc.tcp://host:port。
//
// 安全策略/模式仅透传短名（None/Basic128Rsa15/Basic256/Basic256Sha256 与
// None/Sign/SignAndEncrypt）；Sign/SignAndEncrypt 需要客户端证书，
// 当前版本未实现证书生成与配置，选择后连接将失败（由用户自备证书扩展）。
type OpcUaConfig struct {
	Endpoint       string        // 完整端点地址（opc.tcp://...）
	Host           string        // 主机地址（endpoint 为空时合成用）
	Port           int           // 端口（默认 4840）
	SecurityPolicy string        // 安全策略短名
	SecurityMode   string        // 安全模式短名
	AuthMode       string        // 认证方式：Anonymous / Username
	Username       string        // AuthMode=Username 时必填
	Password       string        // AuthMode=Username 时必填
	AppName        string        // 客户端 ApplicationName（可选）
	TimeoutMS      int           // 单次请求超时毫秒
	Timeout        time.Duration // TimeoutMS 换算的时长
	MaxBatch       int           // 单次 ReadRequest 最大节点数
}

// 安全策略/模式的合法取值，解析时校验并快速失败。
var (
	validSecurityPolicies = map[string]bool{
		"None": true, "Basic128Rsa15": true, "Basic256": true,
		"Basic256Sha256": true, "Aes128Sha256RsaOaep": true, "Aes256Sha256RsaPss": true,
	}
	validSecurityModes = map[string]bool{
		"None": true, "Sign": true, "SignAndEncrypt": true,
	}
)

// opcUaConfigRaw 匹配 protocol_json 原始数据格式（数值字段为 JSON 数字）
type opcUaConfigRaw struct {
	Endpoint       string `json:"endpoint"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	SecurityPolicy string `json:"securityPolicy"`
	SecurityMode   string `json:"securityMode"`
	AuthMode       string `json:"authMode"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	AppName        string `json:"appName"`
	TimeoutMS      int    `json:"timeoutMs"`
	MaxBatch       int    `json:"maxBatch"`
}

// DefaultOpcUaConfig 返回 OPC UA 驱动默认配置。
func DefaultOpcUaConfig() *OpcUaConfig {
	return &OpcUaConfig{
		Host:           "127.0.0.1",
		Port:           4840,
		SecurityPolicy: "None",
		SecurityMode:   "None",
		AuthMode:       "Anonymous",
		TimeoutMS:      5000,
		Timeout:        5 * time.Second,
		MaxBatch:       100,
	}
}

// ParseOpcUaConfig 从 device.protocol_json JSON 字符串解析 OPC UA 配置。
// 未设置的字段使用默认值，非法数值容错回退默认值；安全策略/模式与认证方式
// 取值非法时返回错误（避免连接时收到含糊的握手失败）。
func ParseOpcUaConfig(protocolJSON string) (*OpcUaConfig, error) {
	cfg := DefaultOpcUaConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	var raw opcUaConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("opcua config: invalid JSON: %w", err)
	}
	if raw == (opcUaConfigRaw{}) {
		return cfg, nil
	}

	cfg.Endpoint = raw.Endpoint
	if raw.Host != "" {
		cfg.Host = raw.Host
	}
	if raw.Port > 0 && raw.Port <= 65535 {
		cfg.Port = raw.Port
	}
	if raw.SecurityPolicy != "" {
		cfg.SecurityPolicy = raw.SecurityPolicy
	}
	if raw.SecurityMode != "" {
		cfg.SecurityMode = raw.SecurityMode
	}
	if raw.AuthMode != "" {
		cfg.AuthMode = raw.AuthMode
	}
	cfg.Username = raw.Username
	cfg.Password = raw.Password
	cfg.AppName = raw.AppName
	if raw.TimeoutMS > 0 {
		cfg.TimeoutMS = raw.TimeoutMS
	}
	if raw.MaxBatch > 0 {
		cfg.MaxBatch = raw.MaxBatch
		if cfg.MaxBatch > 1000 {
			cfg.MaxBatch = 1000
		}
	}

	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 5000
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	// 端点地址合成：endpoint 优先，缺失时由 host:port 生成。
	// Host/Port 有默认值兜底，此处不会产生空 host。
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	if cfg.Endpoint == "" {
		cfg.Endpoint = "opc.tcp://" + net.JoinHostPort(strings.TrimSpace(cfg.Host), strconv.Itoa(cfg.Port))
	}
	if !strings.HasPrefix(cfg.Endpoint, "opc.tcp://") {
		return nil, fmt.Errorf("opcua config: endpoint %q must start with opc.tcp://", cfg.Endpoint)
	}

	// 安全策略/模式取值校验
	if !validSecurityPolicies[cfg.SecurityPolicy] {
		return nil, fmt.Errorf("opcua config: unsupported securityPolicy %q", cfg.SecurityPolicy)
	}
	if !validSecurityModes[cfg.SecurityMode] {
		return nil, fmt.Errorf("opcua config: unsupported securityMode %q", cfg.SecurityMode)
	}

	// 认证方式校验
	switch strings.ToLower(cfg.AuthMode) {
	case "", "anonymous":
		cfg.AuthMode = "Anonymous"
	case "username":
		cfg.AuthMode = "Username"
		if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			return nil, fmt.Errorf("opcua config: authMode=Username requires username and password")
		}
	default:
		return nil, fmt.Errorf("opcua config: unsupported authMode %q", cfg.AuthMode)
	}

	return cfg, nil
}

// opts 构建 gopcua 客户端 Option 列表。
// AutoReconnect 必须显式关闭：gopcua 默认开启内部重连，会与采集引擎的
// 指数退避重连形成双循环，且自愈后的旧 SecureChannel 会使后续 Connect
// 返回 "already connected"（见 client.go 注释）。
func (c *OpcUaConfig) opts() []opcua.Option {
	opts := []opcua.Option{
		opcua.SecurityPolicy(c.SecurityPolicy),
		opcua.SecurityModeString(c.SecurityMode),
		opcua.AutoReconnect(false),
		opcua.RequestTimeout(c.Timeout),
		opcua.DialTimeout(c.Timeout),
	}
	if strings.EqualFold(c.AuthMode, "Username") {
		opts = append(opts, opcua.AuthUsername(c.Username, c.Password))
	} else {
		opts = append(opts, opcua.AuthAnonymous())
	}
	if c.AppName != "" {
		opts = append(opts, opcua.ApplicationName(c.AppName))
	}
	return opts
}
