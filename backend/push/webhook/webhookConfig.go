// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package webhook

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const (
	defaultTimeoutSeconds  = 10
	maxTimeoutSeconds      = 120
	defaultWorkers         = 4
	maxWorkers             = 64
	defaultSpoolMaxBatches = 100000
	defaultContentType     = "application/json; charset=utf-8"
	defaultHMACHeader      = "X-Signature"
	defaultHMACPrefix      = "sha256="
	authNone               = "none"
	authBearer             = "bearer"
	authBasic              = "basic"
	authHMAC               = "hmac-sha256"
)

type config struct {
	URL         string `json:"url"`
	Method      string `json:"method"`
	ContentType string `json:"contentType"`
	HeadersJSON string `json:"headersJson"`

	AuthType      string `json:"authType"`
	BearerToken   string `json:"bearerToken"`
	BasicUsername string `json:"basicUsername"`
	BasicPassword string `json:"basicPassword"`
	HMACSecret    string `json:"hmacSecret"`
	HMACHeader    string `json:"hmacHeader"`
	HMACPrefix    string `json:"hmacPrefix"`

	TimeoutSeconds     int  `json:"timeoutSeconds"`
	Workers            int  `json:"workers"`
	SpoolDisabled      bool `json:"spoolDisabled"`
	SpoolMaxBatches    int  `json:"spoolMaxBatches"`
	InsecureSkipVerify bool `json:"insecureSkipVerify"`

	Format  string          `json:"format,omitempty"`
	Mapping json.RawMessage `json:"mapping,omitempty"`
}

func parseConfig(raw string) (*config, error) {
	var cfg config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("http: 配置解析失败: %w", err)
	}
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("http: url 非法: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("http: url 仅支持 http/https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("http: url 缺少主机")
	}
	cfg.URL = u.String()

	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	if cfg.Method == "" {
		cfg.Method = "POST"
	}
	switch cfg.Method {
	case "POST", "PUT", "PATCH":
	default:
		return nil, fmt.Errorf("http: method 仅支持 POST/PUT/PATCH")
	}

	cfg.ContentType = strings.TrimSpace(cfg.ContentType)
	if cfg.ContentType == "" {
		cfg.ContentType = defaultContentType
	}
	if strings.ContainsAny(cfg.ContentType, "\r\n") {
		return nil, fmt.Errorf("http: contentType 含非法字符")
	}

	cfg.AuthType = strings.ToLower(strings.TrimSpace(cfg.AuthType))
	if cfg.AuthType == "" {
		cfg.AuthType = authNone
	}
	switch cfg.AuthType {
	case authNone:
	case authBearer:
		if strings.TrimSpace(cfg.BearerToken) == "" {
			return nil, fmt.Errorf("http: authType=bearer 需要填写 bearerToken")
		}
	case authBasic:
		if cfg.BasicUsername == "" || cfg.BasicPassword == "" {
			return nil, fmt.Errorf("http: authType=basic 需要填写用户名和密码")
		}
	case authHMAC:
		if strings.TrimSpace(cfg.HMACSecret) == "" {
			return nil, fmt.Errorf("http: authType=hmac-sha256 需要填写 hmacSecret")
		}
		cfg.HMACHeader = strings.TrimSpace(cfg.HMACHeader)
		if cfg.HMACHeader == "" {
			cfg.HMACHeader = defaultHMACHeader
		}
		if !validHeaderName(cfg.HMACHeader) {
			return nil, fmt.Errorf("http: hmacHeader %q 不是合法的请求头名", cfg.HMACHeader)
		}
		if cfg.HMACPrefix == "" {
			cfg.HMACPrefix = defaultHMACPrefix
		}
	default:
		return nil, fmt.Errorf("http: 未知的 authType %q", cfg.AuthType)
	}

	if _, err := parseHeaders(cfg.HeadersJSON); err != nil {
		return nil, err
	}

	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = defaultTimeoutSeconds
	}
	if cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > maxTimeoutSeconds {
		return nil, fmt.Errorf("http: timeoutSeconds 超出范围 [1,%d]", maxTimeoutSeconds)
	}
	if cfg.Workers == 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Workers > maxWorkers {
		cfg.Workers = maxWorkers
	}
	if cfg.SpoolMaxBatches <= 0 {
		cfg.SpoolMaxBatches = defaultSpoolMaxBatches
	}
	return &cfg, nil
}

func (c *config) spoolEnabled() bool { return !c.SpoolDisabled }

func (c *config) timeoutSeconds() int { return c.TimeoutSeconds }

func parseHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, fmt.Errorf("http: headersJson 必须是字符串键值对象: %w", err)
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		if !validHeaderName(name) {
			return nil, fmt.Errorf("http: headersJson 的请求头名 %q 非法", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("http: headersJson 的 %q 含非法字符", name)
		}
		out[name] = value
	}
	return out, nil
}

// validHeaderName 只做 RFC 7230 token 的保守校验：字母数字与常见分隔符之外一律拒绝。
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return true
}
