// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 连接与调优常量。
//
// 这些是**内部调参**，不是用户配置项 —— 它们在表单里不出现，改它们要重新
// 编译插件。理由：现场能调的是"连哪台控制器、用什么凭据"，而"每轮最多回退
// 几个点位"这类是驱动内部的安全边界，暴露出去只会让用户在不理解后果的情况下
// 把限流绕过去。
const (
	// defaultMaxConcurrency 单设备并发请求数上限。RWS 每个会话只给 2 个 HTTP
	// 连接，而一轮本只有 2~3 个列表请求，串行完全够用，还能省掉一整层
	// 非竞争/预算计算的复杂度。
	defaultMaxConcurrency = 1

	// defaultMaxRequestsPerSecond 令牌桶容量。控制器硬限流是 <20 req/s，
	// 取 8 是为了给示教器与 RobotStudio 留出余量 —— 抢满带宽的后果是
	// 控制器开始对**所有**客户端回 503，包括正在示教的工人。
	defaultMaxRequestsPerSecond = 8
	maxRequestsPerSecondLimit   = 18

	// defaultMaxFallbackPerRound / defaultFallbackBudget 是单轮回退的条数与
	// 时间双上限，先到先停。
	//
	// 时间上限不是可有可无的：网关给插件的 Read 超时默认 60s
	// （pluginhost 的 readTimeoutMs），而 2000 个"列表里没有"的僵尸点位在
	// 8 req/s 下要 250s 才发得完 —— 网关会先超时、判未连接、重连，然后
	// 重新探测、再来一轮。表现为设备在"已连接/未连接"之间反复跳。
	// 15s 的预算保证最坏情况远离 60s。
	defaultMaxFallbackPerRound = 32
	defaultFallbackBudget      = 15 * time.Second

	// defaultNegativeCacheTTL 点位确认不存在（单点 GET 返回 4xx）后的静默期。
	// 没有它，一个已删除的信号会在每一轮里都产生一次请求。
	defaultNegativeCacheTTL = 60 * time.Second

	// defaultOverrideCacheTTL 列表不覆盖某点位、但单点 GET 成功时的静默期。
	// 这类点位（如被分页挤掉的）必须继续走单点，但不必每轮都先查一次索引。
	defaultOverrideCacheTTL = 300 * time.Second

	// defaultFieldWarnTTL 同一条"这个点位取不到值"的告警的重复间隔。
	//
	// 字段缺失、地址语法错这类条件在**每一轮**都重新成立，不设间隔地打日志
	// 会让网关日志被同一行刷屏（1s 周期 × 几十个点位 = 每秒几十行），
	// 而重复的行不携带任何新信息。间隔过后再打一次，是为了让"配错了"这件事
	// 不会因为错过了第一行日志就永远查不到。
	defaultFieldWarnTTL = 60 * time.Second

	// stickyDisableThreshold / stickyDisableTTL 连续多少次"确实不支持"之后
	// 才粘性禁用一类列表请求，以及禁用多久。404/501 通常意味着这个固件版本
	// 没有该资源，每轮重试只是白烧请求配额。
	stickyDisableThreshold = 3
	stickyDisableTTL       = 300 * time.Second

	// fallbackHintMaxEntries 回退提示表的条目上限，超限清空重建（有界）。
	fallbackHintMaxEntries = 4096

	// responseBodyLimit 单个响应体的读取上限。信号列表在几千点位时约几百 KB，
	// 8MB 是"远超合理值、但仍能挡住失控响应"的位置。
	responseBodyLimit = 8 << 20

	// probeTimeoutCap 探测单个候选端点时的超时上限。
	// 探测要按候选依次尝试（https→http），用配置里的完整超时会让
	// "两个候选都不可达"的 Connect 卡满两倍超时。
	probeTimeoutCap = 3 * time.Second
)

// 三态配置的取值。`useHttps` / `sessionRequest` 用 auto，`insecureSkipVerify` /
// `bulkRead` 只有开关两种状态，但同样走 triBool 以容忍 form-create 回传的
// 布尔或字符串两种形态。
const (
	triAuto  = "auto"
	triTrue  = "true"
	triFalse = "false"
)

// RWS 版本取值。
const (
	versionAuto = "auto"
	version10   = "1.0"
	version20   = "2.0"
)

// Config ABB RWS 协议驱动配置。
type Config struct {
	Endpoint string // 完整端点地址（https://host:port），优先于 host/port
	Host     string // 控制器 IP 或主机名
	Port     int    // 端口，0 表示按 useHttps 取默认（443 / 80）

	UseHTTPS   string // auto | true | false
	RWSVersion string // auto | 1.0 | 2.0

	Username string
	Password string

	TimeoutMS int
	Timeout   time.Duration

	// InsecureSkipVerify 默认 true。ABB 控制器出厂即自签名证书，且 CN 与现场
	// 分配的 IP 常年不符 —— 默认校验证书会让"刚填完表单点测试连接"必然失败，
	// 那是最差的首跑体验。
	InsecureSkipVerify bool

	// BulkRead 开启"批量优先 + 单点回退"。关掉则退化为逐点 GET，
	// 只在排查"是不是批量列表在捣鬼"时才需要。
	BulkRead bool

	// SessionRequest 控制是否在 Connect 时显式创建 RWS 会话
	// （POST /rw/elog/0?action=request）。
	//
	// 它的作用比字面看起来小：Go 的 CookieJar 会自动收下任何响应里的
	// Set-Cookie 并在后续同 host 请求上回发，所以"请求带不带 cookie"这件事
	// 不取决于它。它真正解决的是"某些固件在没有会话 cookie 时对部分资源回
	// 503"，代价是每建立一个驱动实例就占一个会话槽（RWS 有硬上限）。
	// auto 因此在 RWS 1.0 上执行、2.0 上跳过 —— 2.0 走预置 Basic，不需要。
	SessionRequest string

	MaxRequestsPerSecond int

	// 笛卡尔位姿的来源与坐标系。仅当读数走 motion:<unit>/cartesian 时生效。
	CartesianSource     string // robtarget | cartesian
	CartesianTool       string
	CartesianWobj       string
	CartesianCoordinate string
}

// rwsConfigRaw 匹配 protocol_json 的原始数据格式。
//
// 三态字段用 json.RawMessage 接：form-create 的 select 会让它们以字符串
// （"auto"）或布尔（true）两种形态过线，用 bool 接会在用户选了"自动"时
// 直接报 JSON 类型错误。
type rwsConfigRaw struct {
	Endpoint string `json:"endpoint"`
	Host     string `json:"host"`
	Port     int    `json:"port"`

	UseHTTPS   json.RawMessage `json:"useHttps"`
	RWSVersion string          `json:"rwsVersion"`

	Username string `json:"username"`
	// Password 用指针：必须区分"字段没填"（沿用默认密码）与"显式填了空串"
	// （免认证的控制器）。用 string 时两者都是 ""，只能二选一 —— 要么没法配置
	// 空密码，要么用户不动表单就丢掉出厂默认密码 robotics。
	Password *string `json:"password"`

	TimeoutMS          int             `json:"timeoutMs"`
	InsecureSkipVerify json.RawMessage `json:"insecureSkipVerify"`
	BulkRead           json.RawMessage `json:"bulkRead"`
	SessionRequest     json.RawMessage `json:"sessionRequest"`

	MaxRequestsPerSecond int `json:"maxRequestsPerSecond"`

	CartesianSource     string `json:"cartesianSource"`
	CartesianTool       string `json:"cartesianTool"`
	CartesianWobj       string `json:"cartesianWobj"`
	CartesianCoordinate string `json:"cartesianCoordinate"`
}

// ConfigFieldNames 返回 protocol_json 接受的字段名（rwsConfigRaw 的 json tag）。
//
// 导出给插件入口的测试用：表单（main.go 的 formRWS）里的每个 field 必须与
// 它们逐一对应。表单里的 field 就是 Connect 收到的 protocolJSON 的键，
// 两边错开一个字母的症状是"用户填了那个参数，但它根本不生效" ——
// 没有报错、没有日志，表单看起来一切正常，只能靠测试挡。
//
// 之所以导出这个窄接口而不是整个 rwsConfigRaw，是为了不让"原始 JSON 形状"
// 变成包外可依赖的 API：有访问者就会有人拿它去构造配置，而那条路应该走
// ParseConfig（它带校验与钳制）。
func ConfigFieldNames() []string {
	rt := reflect.TypeOf(rwsConfigRaw{})
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DefaultConfig 返回 ABB RWS 驱动默认配置。
func DefaultConfig() *Config {
	return &Config{
		Host:                 "127.0.0.1",
		Port:                 0,
		UseHTTPS:             triAuto,
		RWSVersion:           versionAuto,
		Username:             "Default User",
		Password:             "robotics",
		TimeoutMS:            5000,
		Timeout:              5 * time.Second,
		InsecureSkipVerify:   true,
		BulkRead:             true,
		SessionRequest:       triAuto,
		MaxRequestsPerSecond: defaultMaxRequestsPerSecond,
		CartesianSource:      "robtarget",
		CartesianTool:        "tool0",
		CartesianWobj:        "wobj0",
		CartesianCoordinate:  "Base",
	}
}

// ParseConfig 从 device.protocol_json 解析 ABB RWS 配置。
//
// 未设置的字段用默认值；非法数值容错回退默认值（对齐 opcuadrv/config.go 的
// 风格）；取值的**枚举**（useHttps / rwsVersion / cartesianSource）非法时返回
// 错误 —— 含糊的取值会在连接期变成一句指不到根因的失败。
func ParseConfig(protocolJSON string) (*Config, error) {
	cfg := DefaultConfig()
	if strings.TrimSpace(protocolJSON) == "" {
		return cfg, nil
	}

	var raw rwsConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("abb config: invalid JSON: %w", err)
	}

	if s := strings.TrimSpace(raw.Endpoint); s != "" {
		cfg.Endpoint = s
	}
	if s := strings.TrimSpace(raw.Host); s != "" {
		cfg.Host = s
	}
	if raw.Port > 0 && raw.Port <= 65535 {
		cfg.Port = raw.Port
	}

	if s := strings.TrimSpace(raw.RWSVersion); s != "" {
		cfg.RWSVersion = s
	}
	if s := strings.TrimSpace(raw.Username); s != "" {
		cfg.Username = s
	}
	if raw.Password != nil {
		cfg.Password = *raw.Password // 显式空串是合法值，不是"未设置"
	}

	if raw.TimeoutMS > 0 {
		cfg.TimeoutMS = raw.TimeoutMS
	}
	if raw.MaxRequestsPerSecond > 0 {
		cfg.MaxRequestsPerSecond = raw.MaxRequestsPerSecond
	}

	var err error
	if cfg.UseHTTPS, err = triBool(raw.UseHTTPS, triAuto, "useHttps"); err != nil {
		return nil, err
	}
	var skip string
	if skip, err = triBool(raw.InsecureSkipVerify, triTrue, "insecureSkipVerify"); err != nil {
		return nil, err
	}
	cfg.InsecureSkipVerify = skip != triFalse

	var bulk string
	if bulk, err = triBool(raw.BulkRead, triTrue, "bulkRead"); err != nil {
		return nil, err
	}
	cfg.BulkRead = bulk != triFalse

	if cfg.SessionRequest, err = triBool(raw.SessionRequest, triAuto, "sessionRequest"); err != nil {
		return nil, err
	}

	if s := strings.TrimSpace(raw.CartesianSource); s != "" {
		cfg.CartesianSource = s
	}
	if s := strings.TrimSpace(raw.CartesianTool); s != "" {
		cfg.CartesianTool = s
	}
	if s := strings.TrimSpace(raw.CartesianWobj); s != "" {
		cfg.CartesianWobj = s
	}
	if s := strings.TrimSpace(raw.CartesianCoordinate); s != "" {
		cfg.CartesianCoordinate = s
	}

	// ---- 钳制 ----
	cfg.TimeoutMS = clamp(cfg.TimeoutMS, 200, 30000)
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	cfg.MaxRequestsPerSecond = clamp(cfg.MaxRequestsPerSecond, 1, maxRequestsPerSecondLimit)

	// ---- 枚举校验 ----
	if cfg.RWSVersion != versionAuto && cfg.RWSVersion != version10 && cfg.RWSVersion != version20 {
		return nil, fmt.Errorf("abb config: unsupported rwsVersion %q (可用：auto / 1.0 / 2.0)", cfg.RWSVersion)
	}
	switch strings.ToLower(cfg.CartesianSource) {
	case "robtarget":
		cfg.CartesianSource = "robtarget"
	case "cartesian":
		cfg.CartesianSource = "cartesian"
	default:
		return nil, fmt.Errorf("abb config: unsupported cartesianSource %q (可用：robtarget / cartesian)", cfg.CartesianSource)
	}
	switch strings.ToLower(cfg.CartesianCoordinate) {
	case "base":
		cfg.CartesianCoordinate = "Base"
	case "world":
		cfg.CartesianCoordinate = "World"
	case "tool":
		cfg.CartesianCoordinate = "Tool"
	case "wobj":
		cfg.CartesianCoordinate = "Wobj"
	default:
		return nil, fmt.Errorf("abb config: unsupported cartesianCoordinate %q (可用：Base / World / Tool / Wobj)", cfg.CartesianCoordinate)
	}

	// 注意：这里**不校验** host/endpoint 是否为空。Host 有 127.0.0.1 兜底
	// （对齐 opcuadrv，且设备表单里 host 本就是必填），所以"两者同时为空"
	// 是构造不出来的状态，为它写一条永远不会触发的错误只会误导读者。
	return cfg, nil
}

// candidateBaseURLs 按配置生成候选 base URL，按优先级排列。
//
// 探测会依次尝试，第一个能通的胜出。顺序有意义：HTTPS 在前，因为
// OmniCore 默认只开 HTTPS；而 useHttps=auto 时回落到 HTTP 的前提是
// **HTTPS 端口根本不可达**，不是"HTTPS 握手失败"（见 session.go 的 probe）。
func (c *Config) candidateBaseURLs() []string {
	if ep := strings.TrimSpace(c.Endpoint); ep != "" {
		return []string{normalizeBaseURL(ep)}
	}

	host := strings.TrimSpace(c.Host)
	// 允许用户把 "192.168.1.10:5466" 整个写进 host 字段（现场很常见）。
	hostPort := 0
	if h, p, err := net.SplitHostPort(host); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n <= 65535 {
			host, hostPort = h, n
		}
	}
	port := c.Port
	if port == 0 {
		port = hostPort
	}

	switch c.UseHTTPS {
	case triTrue:
		return []string{"https://" + net.JoinHostPort(host, strconv.Itoa(orDefault(port, 443)))}
	case triFalse:
		return []string{"http://" + net.JoinHostPort(host, strconv.Itoa(orDefault(port, 80)))}
	default: // auto
		if port > 0 {
			hp := net.JoinHostPort(host, strconv.Itoa(port))
			return []string{"https://" + hp, "http://" + hp}
		}
		return []string{
			"https://" + net.JoinHostPort(host, "443"),
			"http://" + net.JoinHostPort(host, "80"),
		}
	}
}

// normalizeBaseURL 补全端点地址：缺 scheme 时按 https 补（OmniCore 的默认），
// 并去掉结尾斜杠，避免后面拼出 "https://host//rw/system"。
func normalizeBaseURL(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// triBool 解析三态/布尔配置项。
//
// form-create 的 select 组件回传的是字符串（"auto"/"true"/"false"），
// 而手写的 protocol_json（以及 API 直连调用）习惯传 JSON 布尔。
// 两种形态都接受，其余一律报错 —— 静默当成默认值会让用户以为改生效了。
func triBool(raw json.RawMessage, def, field string) (string, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return def, nil
	}
	switch strings.ToLower(s) {
	case `"auto"`, "auto":
		return triAuto, nil
	case `"true"`, "true":
		return triTrue, nil
	case `"false"`, "false":
		return triFalse, nil
	}
	return "", fmt.Errorf("abb config: %s = %s 不是合法取值（可用：auto / true / false）", field, s)
}
