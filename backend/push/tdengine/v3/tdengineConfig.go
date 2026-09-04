// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 默认值与上限
const (
	// defaultPort TDengine 3.x WebSocket(taosadapter)默认端口
	defaultPort = 6041
	// defaultBatchWorkers 并发写入 worker 数缺省值
	defaultBatchWorkers = 4
	// maxBatchWorkers 写入 worker 数上限,防止配置异常导致 goroutine 失控
	maxBatchWorkers = 64
	// defaultDatabase 目标数据库名缺省值
	defaultDatabase = "iot"
	// defaultStable 超级表名缺省值(单张超级表,名固定)
	defaultStable = "s_hand"

	// defaultSpoolMaxBatches 断网本地缓存最大批次上限缺省值
	defaultSpoolMaxBatches = 100000

	// defaultBatchRows 单条聚合 INSERT 的最大记录行数缺省值。
	// 聚合把「每设备每轮一次往返」合并为「攒批后一次往返」,行数越大往返越少、
	// 吞吐越高,但单条 SQL 文本与失败重放粒度也越大。
	defaultBatchRows = 2000
	// defaultBatchInterval 攒批等待时间上限:未达 batchRows 时按此间隔强制刷出,
	// 约束聚合引入的额外写入延迟。
	defaultBatchInterval = 200 * time.Millisecond
	// maxBatchRows 单条聚合 INSERT 行数上限:防配置异常拼出超 taosadapter 单条 SQL
	// 长度限制(约 1MB)的巨型语句,导致写必然失败并阻塞补发队列。
	maxBatchRows = 5000
)

// tdengineConfig TDengine 3.x 推送通道配置(解析自 push_channel.config_json)。
//
// 连接走 taosWS 驱动(纯 Go WebSocket → taosadapter:6041),不依赖原生客户端,
// 与本项目 CGO-free 构建保持一致。
//
// 示例:
//
//	{"host":"127.0.0.1","port":"6041","username":"root","password":"taosdata",
//	 "database":"iot","stable":"collected_data","batchWorkers":4,
//	 "batchRows":2000,"batchIntervalMs":200}
type tdengineConfig struct {
	// Host taosadapter 主机名/IP(不含协议前缀)
	Host string `json:"host"`
	// Port taosadapter WebSocket 端口(默认 6041),兼容字符串与数字写法
	Port portStr `json:"port"`
	// Username 用户名(默认 root)
	Username string `json:"username"`
	// Password 密码(默认 taosdata)
	Password string `json:"password"`
	// Database 目标数据库名(默认 iot)。库不存在时通道启动自动创建。
	Database string `json:"database"`
	// Stable 超级表名(默认 s_hand)。单张超级表,表名固定,value 按 Kind 分列
	// (value_bool/value_int/value_float/value_str)保留原生类型入库。
	Stable string `json:"stable"`
	// BatchWorkers 并发写入 worker 数(默认 4,clamp [1,64])。
	BatchWorkers int `json:"batchWorkers"`
	// BatchRows 单条聚合 INSERT 的最大记录行数(缺省/≤0 用 defaultBatchRows)。
	// 聚合把每设备每轮的独立写入合并为攒批后一次往返;设为 1 退化为单批一条 INSERT。
	BatchRows int `json:"batchRows"`
	// BatchIntervalMs 攒批最大等待毫秒数(缺省/≤0 用 defaultBatchInterval)。
	// 未达 BatchRows 时按此间隔强制刷出,约束写入延迟。
	BatchIntervalMs int `json:"batchIntervalMs"`
	// UseSSL 是否启用 TLS(DSN 用 wss://)。
	UseSSL bool `json:"useSSL"`

	// SpoolDisabled 关闭断网本地缓存(默认启用,复用 push_outbox)。
	// 关闭后 TDengine 不可达期间回退为「丢最旧」的纯内存行为(见 tdengineChannel.Enqueue)。
	SpoolDisabled bool `json:"spoolDisabled"`
	// SpoolMaxBatches 本地缓存最大批次上限(缺省/≤0 用 defaultSpoolMaxBatches)。
	SpoolMaxBatches int `json:"spoolMaxBatches"`

	// safeDB / safeStable 消毒后的数据库名与超级表名(构建 SQL 用,parseConfig 时计算)
	safeDB     string
	safeStable string
}

// parseConfig 解析 config_json 为 tdengine 配置
func parseConfig(configJSON string) (*tdengineConfig, error) {
	var cfg tdengineConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("tdengine: parse config failed: %w", err)
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("tdengine: config host is required")
	}
	if cfg.Port != "" {
		if _, err := strconv.Atoi(string(cfg.Port)); err != nil {
			return nil, fmt.Errorf("tdengine: config port %q invalid", cfg.Port)
		}
	}
	if cfg.Username == "" {
		cfg.Username = "root"
	}
	if cfg.Password == "" {
		cfg.Password = "taosdata"
	}
	if cfg.Database == "" {
		cfg.Database = defaultDatabase
	}
	if cfg.Stable == "" {
		cfg.Stable = defaultStable
	}
	cfg.safeDB = sanitizeIdent(cfg.Database)
	cfg.safeStable = sanitizeIdent(cfg.Stable)
	return &cfg, nil
}

// spoolEnabled 本地缓存是否启用(缺省启用,仅显式 spoolDisabled=true 时关闭)。
func (c *tdengineConfig) spoolEnabled() bool {
	return !c.SpoolDisabled
}

// spoolBatchCap 返回实际生效的缓存批次上限,缺省/≤0 回落默认值。
func (c *tdengineConfig) spoolBatchCap() int {
	if c.SpoolMaxBatches <= 0 {
		return defaultSpoolMaxBatches
	}
	return c.SpoolMaxBatches
}

// batchWorkers 返回实际生效的写入 worker 数:缺省/≤0 回落默认值,并 clamp 上限。
func (c *tdengineConfig) batchWorkers() int {
	n := c.BatchWorkers
	if n < 1 {
		return defaultBatchWorkers
	}
	if n > maxBatchWorkers {
		return maxBatchWorkers
	}
	return n
}

// batchRows 返回单条聚合 INSERT 的最大记录行数:缺省/≤0 回落默认值,并 clamp 上限。
func (c *tdengineConfig) batchRows() int {
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
func (c *tdengineConfig) batchInterval() time.Duration {
	if c.BatchIntervalMs <= 0 {
		return defaultBatchInterval
	}
	return time.Duration(c.BatchIntervalMs) * time.Millisecond
}

// port 返回实际端口,缺省回落默认 6041。
func (c *tdengineConfig) port() string {
	if c.Port != "" {
		return string(c.Port)
	}
	return strconv.Itoa(defaultPort)
}

// endpoint 返回连接地址(用于状态展示),如 ws://127.0.0.1:6041。
func (c *tdengineConfig) endpoint() string {
	scheme := "ws"
	if c.UseSSL {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s:%s", scheme, c.Host, c.port())
}

// dsn 构建 taosWS 驱动连接串。
//
// 注意:DSN 不携带数据库名,所有表名一律使用 {db}.{table} 全限定写法。
// 这样避免依赖 USE 切换数据库(database/sql 连接池中 USE 只对单条连接生效),
// 且建库建表(IF NOT EXISTS)与写入均天然幂等。
func (c *tdengineConfig) dsn() string {
	protocol := "ws"
	if c.UseSSL {
		protocol = "wss"
	}
	// 用户名/密码可能含 @ : 等特殊字符,按驱动要求 percent-encode(驱动侧 QueryUnescape 还原)
	user := url.QueryEscape(c.Username)
	pass := url.QueryEscape(c.Password)
	return fmt.Sprintf("%s:%s@%s(%s:%s)/?readTimeout=30s&writeTimeout=10s&autoReconnect=true&chanLength=16",
		user, pass, protocol, c.Host, c.port())
}

// ensureSchemaSQL 返回建库建表语句列表(启动与重连后执行,均幂等)。
// 单张超级表 {prefix}(默认 s_hand),value 按 Kind 落到值族对应列、保留原生类型:
// value_bool BOOL / value_int BIGINT / value_float DOUBLE / value_str NCHAR,
// 每行恰一列非空、其余 NULL;value_kind 列记录声明类别,作普通列而非 tag
// (Kind 可能随地址重配变化,tag 一经创建不可改会陈旧)。
// 注意:`time` 是 TDengine 保留关键字,列名必须用反引号转义,勿删。
//
// 列/tag 名统一 snake_case(与数据库命名规范一致)。
// 每条采集记录经 `INSERT ... USING {stable} TAGS(...)` 自动创建子表,
// 子表名 = deviceId_deviceAddressId。tag 仅稳定标识 device_id / device_address_id,
// 名称可变不作 tag(子表 tag 值不可变更,存名称会陈旧),名称查询/展示走设备配置关联。
// 值列宽度与写入侧截断保持一致:value_str → maxValueStr、value_kind → maxKindLen、
// tag → maxTagLen(sqlbuilder.go)。
func (c *tdengineConfig) ensureSchemaSQL() []string {
	tags := fmt.Sprintf("TAGS (device_id NCHAR(%d), device_address_id NCHAR(%d))", maxTagLen, maxTagLen)
	stable := c.safeDB + "." + c.safeStable
	return []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", c.safeDB),
		fmt.Sprintf("CREATE STABLE IF NOT EXISTS %s (`time` TIMESTAMP, value_bool BOOL, value_int BIGINT, value_float DOUBLE, value_str NCHAR(%d), value_kind NCHAR(%d), quality INT) %s", stable, maxValueStr, maxKindLen, tags),
	}
}

// portStr 兼容字符串/数字形式的端口配置
type portStr string

// UnmarshalJSON 同时接受 "6041" 与 6041 两种 JSON 类型
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
	return fmt.Errorf("tdengine: invalid port %s", string(b))
}

// sanitizeIdent 将字符串消毒为合法 SQL 标识符(TDengine 表名/库名仅允许 [A-Za-z0-9_])。
// 非法字符替换为下划线;首字符为数字时补下划线前缀。
func sanitizeIdent(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_') // 首字符数字:补下划线
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
