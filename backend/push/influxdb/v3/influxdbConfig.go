package v3

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// 默认值与上限
const (
	// defaultPort InfluxDB 3.x Core/Enterprise 默认 HTTP 端口
	defaultPort = 8181
	// defaultBatchWorkers 并发写入 worker 数缺省值
	defaultBatchWorkers = 4
	// maxBatchWorkers 写入 worker 数上限,防止配置异常导致 goroutine 失控
	maxBatchWorkers = 64
	// defaultDatabase 目标数据库名缺省值
	defaultDatabase = "iot"
	// defaultMeasurement 默认 measurement 名
	defaultMeasurement = "s_hand"

	// defaultSpoolMaxBatches 断网本地缓存最大批次上限缺省值
	defaultSpoolMaxBatches = 100000

	// defaultBatchRows 单个 HTTP POST body 的最大 point 行数缺省值。
	// 聚合把「每设备每轮一次往返」合并为「攒批后一次往返」,行数越大往返越少、
	// 吞吐越高,但单次请求体与失败重放粒度也越大。
	defaultBatchRows = 2000
	// defaultBatchInterval 攒批等待时间上限:未达 batchRows 时按此间隔强制刷出,
	// 约束聚合引入的额外写入延迟。
	defaultBatchInterval = 200 * time.Millisecond
	// maxBatchRows 单次请求 body 行数上限:防配置异常拼出超 InfluxDB 单请求
	// 体量限制的巨型 body,导致写必然失败并阻塞补发队列。
	maxBatchRows = 5000
)

// influxdbConfig InfluxDB 3.x 推送通道配置(解析自 push_channel.config_json)。
//
// 连接走标准库 net/http + Line Protocol 写入 POST /api/v3/write_lp,
// 鉴权 Authorization: Bearer {token},零新增外部依赖、纯 Go、CGO-free。
//
// 示例:
//
//	{"host":"127.0.0.1","port":"8181","token":"<token>","database":"iot",
//	 "measurement":"collected_data","batchWorkers":4,"batchRows":2000,
//	 "batchIntervalMs":200}
type influxdbConfig struct {
	// Host InfluxDB 3.x HTTP 主机名/IP(不含协议前缀,必填)。
	Host string `json:"host"`
	// Port InfluxDB 3.x HTTP 端口(默认 8181),兼容字符串与数字写法。
	Port portStr `json:"port"`
	// Token API 鉴权 token(InfluxDB 3 所有 API 均要求 Bearer token,必填)。
	Token string `json:"token"`
	// Database 目标数据库名(默认 iot)。库不存在时通道启动 best-effort 自动创建
	// (需 admin 权限;非 admin 令牌视为预置库,不阻塞连接)。
	Database string `json:"database"`
	// Measurement 目标 measurement 名(默认 s_hand)。
	// InfluxDB 为 schemaless,无需预建表,写入时自动建。
	Measurement string `json:"measurement"`
	// BatchWorkers 并发写入 worker 数(默认 4,clamp [1,64])。
	BatchWorkers int `json:"batchWorkers"`
	// BatchRows 单个 HTTP 请求的最大 point 行数(缺省/≤0 用 defaultBatchRows)。
	// 聚合把每设备每轮的独立写入合并为攒批后一次往返;设为 1 退化为单批一次请求。
	BatchRows int `json:"batchRows"`
	// BatchIntervalMs 攒批最大等待毫秒数(缺省/≤0 用 defaultBatchInterval)。
	// 未达 BatchRows 时按此间隔强制刷出,约束写入延迟。
	BatchIntervalMs int `json:"batchIntervalMs"`
	// UseSSL 是否启用 TLS(https://)。
	UseSSL bool `json:"useSSL"`
	// InsecureSkipVerify 跳过 TLS 证书校验(自签证书测试环境用,默认 false)。
	InsecureSkipVerify bool `json:"insecureSkipVerify"`

	// SpoolDisabled 关闭断网本地缓存(默认启用,复用 push_outbox)。
	// 关闭后 InfluxDB 不可达期间回退为「丢最旧」的纯内存行为(见 influxdbChannel.Enqueue)。
	SpoolDisabled bool `json:"spoolDisabled"`
	// SpoolMaxBatches 本地缓存最大批次上限(缺省/≤0 用 defaultSpoolMaxBatches)。
	SpoolMaxBatches int `json:"spoolMaxBatches"`
}

// parseConfig 解析 config_json 为 influxdb 配置
func parseConfig(configJSON string) (*influxdbConfig, error) {
	var cfg influxdbConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("influxdb: parse config failed: %w", err)
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("influxdb: config token is required")
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("influxdb: config host is required")
	}
	if cfg.Port != "" {
		if _, err := strconv.Atoi(string(cfg.Port)); err != nil {
			return nil, fmt.Errorf("influxdb: config port %q invalid", cfg.Port)
		}
	}
	if cfg.Database == "" {
		cfg.Database = defaultDatabase
	}
	if cfg.Measurement == "" {
		cfg.Measurement = defaultMeasurement
	}
	return &cfg, nil
}

// spoolEnabled 本地缓存是否启用(缺省启用,仅显式 spoolDisabled=true 时关闭)。
func (c *influxdbConfig) spoolEnabled() bool {
	return !c.SpoolDisabled
}

// spoolBatchCap 返回实际生效的缓存批次上限,缺省/≤0 回落默认值。
func (c *influxdbConfig) spoolBatchCap() int {
	if c.SpoolMaxBatches <= 0 {
		return defaultSpoolMaxBatches
	}
	return c.SpoolMaxBatches
}

// batchWorkers 返回实际生效的写入 worker 数:缺省/≤0 回落默认值,并 clamp 上限。
func (c *influxdbConfig) batchWorkers() int {
	n := c.BatchWorkers
	if n < 1 {
		return defaultBatchWorkers
	}
	if n > maxBatchWorkers {
		return maxBatchWorkers
	}
	return n
}

// batchRows 返回单个 HTTP 请求的最大 point 行数:缺省/≤0 回落默认值,并 clamp 上限。
func (c *influxdbConfig) batchRows() int {
	n := c.BatchRows
	if n < 1 {
		return defaultBatchRows
	}
	if n > maxBatchRows {
		return maxBatchRows
	}
	return n
}

// batchInterval 返回攒批最大等待时长:缺省/≤0 回落默认值。
func (c *influxdbConfig) batchInterval() time.Duration {
	if c.BatchIntervalMs <= 0 {
		return defaultBatchInterval
	}
	return time.Duration(c.BatchIntervalMs) * time.Millisecond
}

// port 返回实际端口,缺省回落默认 8181。
func (c *influxdbConfig) port() string {
	if c.Port != "" {
		return string(c.Port)
	}
	return strconv.Itoa(defaultPort)
}

// baseURL 返回服务根地址(用于拼接各端点与状态展示),如 http://127.0.0.1:8181。
func (c *influxdbConfig) baseURL() string {
	scheme := "http"
	if c.UseSSL {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%s", scheme, c.Host, c.port())
}

// healthURL 返回健康检查端点(GET /health,带 token,200 视为连通)
func (c *influxdbConfig) healthURL() string {
	return c.baseURL() + "/health"
}

// configureDatabaseURL 返回建库端点(POST /api/v3/configure/database)
func (c *influxdbConfig) configureDatabaseURL() string {
	return c.baseURL() + "/api/v3/configure/database"
}

// writeURL 返回 Line Protocol 写入端点。
// db 经 url.QueryEscape 编码(库名通常为 [a-zA-Z0-9-],防御性转义);
// precision=millisecond 与 CollectedAt 的毫秒精度时间戳一致。
func (c *influxdbConfig) writeURL() string {
	return c.baseURL() + "/api/v3/write_lp?db=" + url.QueryEscape(c.Database) + "&precision=millisecond"
}

// portStr 兼容字符串/数字形式的端口配置
type portStr string

// UnmarshalJSON 同时接受 "8181" 与 8181 两种 JSON 类型
func (p *portStr) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*p = portStr(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*p = portStr(strconv.FormatInt(n, 10))
		return nil
	}
	return fmt.Errorf("influxdb: invalid port %s", string(b))
}
