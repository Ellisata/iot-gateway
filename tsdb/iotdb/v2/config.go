// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package iotdb 提供 Apache IoTDB 2.x(树模型)的时序写入端。
//
// 路径里的 v2 指**上游服务端 API 版本**:使用官方 Go 客户端
// github.com/apache/iotdb-client-go/v2,只面向 IoTDB 2.x 服务端。
// 1.3.x 走另一条官方客户端线(iotdb-client-go@v1.3.x,建库用 SET STORAGE GROUP),
// 官方不支持 2.x 客户端连 1.x 服务端;若需支持 1.x,应新增 tsdb/iotdb/v1 并复用
// 本包的负载映射,不要在这里加版本开关。
package iotdb

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/apache/iotdb-client-go/v2/client"
)

// 默认值与上限
const (
	// defaultPort IoTDB 原生 RPC 默认端口
	defaultPort = 6667
	// defaultDatabase 默认数据库路径(IoTDB 树模型,以 root 开头)
	defaultDatabase = "root.iot"
	// maxDatabaseLen IoTDB 数据库路径(含 root.)的长度上限
	maxDatabaseLen = 64
	// defaultBatchWorkers 并发写入 worker 数缺省值
	defaultBatchWorkers = 4
	// maxBatchWorkers 写入 worker 数上限,防止配置异常导致 goroutine 失控
	maxBatchWorkers = 64
	// defaultSpoolMaxBatches 断网本地缓存最大批次上限缺省值
	defaultSpoolMaxBatches = 100000
	// defaultBatchRows 单个 insertRecords 请求的最大记录行数缺省值
	defaultBatchRows = 2000
	// defaultBatchInterval 攒批等待时间上限
	defaultBatchInterval = 200 * time.Millisecond
	// maxBatchRows 单次 insertRecords 行数上限,防配置异常拼出超大请求
	maxBatchRows = 5000
	// connectTimeoutMs 建立单条会话的连接超时
	connectTimeoutMs = 5000
	// getSessionTimeoutMs 从会话池取连接的等待上限
	getSessionTimeoutMs = 5000
)

// Config IoTDB 2.x 推送通道配置(解析自 push_channel.config_json)。
//
// 连接走官方 Go 原生客户端(纯 Go Thrift,默认 6667),不依赖 REST 服务。
// 目标为树模型:每个 (deviceID, deviceAddressID) 映射为一个设备路径,
// value 按 Kind 落到 value_bool / value_int / value_float / value_str 测量列。
//
// 示例:
//
//	{"host":"127.0.0.1","port":"6667","username":"root","password":"root",
//	 "database":"root.iot","batchWorkers":4,"batchRows":2000,"batchIntervalMs":200}
type Config struct {
	// Host IoTDB 主机名/IP(不含协议前缀)。配置了 nodes 时忽略。
	Host string `json:"host"`
	// Port 原生 RPC 端口(默认 6667),兼容字符串与数字写法。
	Port portStr `json:"port"`
	// Username 用户名(默认 root)。
	Username string `json:"username"`
	// Password 密码(默认 root)。
	Password string `json:"password"`
	// Nodes 集群节点列表(host:port)。非空时按集群会话连接,忽略 Host/Port。
	// 兼容 JSON 数组与英文逗号分隔的字符串两种写法。
	Nodes nodeList `json:"nodes"`
	// Database 数据库路径(默认 root.iot)。首段不是 root 时自动补 root。
	// 库不存在时通道启动自动创建;序列依赖服务端 enable_auto_create_schema=true
	// (2.x 默认)自动建。路径(含 root.)不得超过 64 字符。
	Database string `json:"database"`
	// RPCCompression 是否启用 Thrift RPC 压缩(默认 false)。
	RPCCompression bool `json:"rpcCompression"`
	// BatchWorkers 并发写入 worker 数(默认 4,clamp [1,64])。
	BatchWorkers int `json:"batchWorkers"`
	// BatchRows 单个 insertRecords 请求的最大记录行数(缺省/≤0 用 defaultBatchRows)。
	BatchRows int `json:"batchRows"`
	// BatchIntervalMs 攒批最大等待毫秒数(缺省/≤0 用 defaultBatchInterval)。
	BatchIntervalMs int `json:"batchIntervalMs"`

	// TLS 客户端证书校验。三项均留空且未勾选 insecureSkipVerify 时走明文。
	// CAFile 为可选的 CA 证书路径;CertFile/KeyFile 成对提供时启用 mTLS 客户端认证。
	TLSCAFile   string `json:"tlsCAFile"`
	TLSCertFile string `json:"tlsCertFile"`
	TLSKeyFile  string `json:"tlsKeyFile"`
	// InsecureSkipVerify 跳过 TLS 证书校验(自签证书测试环境用,默认 false)。
	InsecureSkipVerify bool `json:"insecureSkipVerify"`

	// SpoolDisabled 关闭断网本地缓存(默认启用,复用 push_outbox)。
	SpoolDisabled bool `json:"spoolDisabled"`
	// SpoolMaxBatches 本地缓存最大批次上限(缺省/≤0 用 defaultSpoolMaxBatches)。
	SpoolMaxBatches int `json:"spoolMaxBatches"`

	// safeDB 消毒后的数据库路径(建库与拼设备路径用,ParseConfig 时计算)
	safeDB string
}

// ParseConfig 解析 config_json 为 iotdb 配置
func ParseConfig(configJSON string) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("iotdb: parse config failed: %w", err)
	}
	if len(cfg.Nodes) == 0 && cfg.Host == "" {
		return nil, fmt.Errorf("iotdb: config host or nodes is required")
	}
	if cfg.Port != "" {
		if _, err := strconv.Atoi(string(cfg.Port)); err != nil {
			return nil, fmt.Errorf("iotdb: config port %q invalid", cfg.Port)
		}
	}
	for _, node := range cfg.Nodes {
		if _, _, err := net.SplitHostPort(node); err != nil {
			return nil, fmt.Errorf("iotdb: config node %q invalid (want host:port): %w", node, err)
		}
	}
	if cfg.Username == "" {
		cfg.Username = "root"
	}
	if cfg.Password == "" {
		cfg.Password = "root"
	}
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return nil, fmt.Errorf("iotdb: tlsCertFile and tlsKeyFile must be provided together")
	}
	if cfg.Database == "" {
		cfg.Database = defaultDatabase
	}
	db, err := normalizeDatabasePath(cfg.Database)
	if err != nil {
		return nil, err
	}
	cfg.safeDB = db
	return &cfg, nil
}

// SpoolEnabled 本地缓存是否启用(缺省启用,仅显式 spoolDisabled=true 时关闭)。
func (c *Config) SpoolEnabled() bool { return !c.SpoolDisabled }

// SpoolBatchCap 返回实际生效的缓存批次上限,缺省/≤0 回落默认值。
func (c *Config) SpoolBatchCap() int {
	if c.SpoolMaxBatches <= 0 {
		return defaultSpoolMaxBatches
	}
	return c.SpoolMaxBatches
}

// batchWorkers 返回实际生效的写入 worker 数:缺省/≤0 回落默认值,并 clamp 上限。
func (c *Config) batchWorkers() int {
	n := c.BatchWorkers
	if n < 1 {
		return defaultBatchWorkers
	}
	if n > maxBatchWorkers {
		return maxBatchWorkers
	}
	return n
}

// batchRows 返回单次请求最大行数:缺省/≤0 回落默认值,并 clamp 上限。
func (c *Config) batchRows() int {
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
func (c *Config) batchInterval() time.Duration {
	if c.BatchIntervalMs <= 0 {
		return defaultBatchInterval
	}
	return time.Duration(c.BatchIntervalMs) * time.Millisecond
}

// port 返回实际端口,缺省回落默认 6667。
func (c *Config) port() string {
	if c.Port != "" {
		return string(c.Port)
	}
	return strconv.Itoa(defaultPort)
}

// endpoint 返回连接目标(用于状态展示),集群模式为逗号分隔的节点列表。
func (c *Config) endpoint() string {
	if len(c.Nodes) > 0 {
		return strings.Join(c.Nodes, ",")
	}
	return net.JoinHostPort(c.Host, c.port())
}

// databasePath 返回实际写入的数据库路径。
// ParseConfig 之后取消毒后的 safeDB;直接构造 Config 时回落到原始值/默认库。
func (c *Config) databasePath() string {
	if c.safeDB != "" {
		return c.safeDB
	}
	if c.Database != "" {
		return c.Database
	}
	return defaultDatabase
}

// createDatabaseSQL 返回建库语句(启动与重连后执行)。
//
// 树模型的 CREATE DATABASE **不支持 IF NOT EXISTS**(那是表模型语法,2.0 树模型
// 会把 IF 当成路径解析并报 "mismatched input 'IF' expecting ROOT")。幂等性由
// ensureDatabase 处理:重复建库返回状态码 501(DatabaseAlreadyExists)视为成功。
// 序列本身由服务端 enable_auto_create_schema(2.x 默认 true)在写入时自动创建。
func (c *Config) createDatabaseSQL() string {
	return "CREATE DATABASE " + c.databasePath()
}

// tlsConfig 把配置转换为客户端 TLS 参数;未启用 TLS 时返回 nil。
func (c *Config) tlsConfig() *client.TLSConfig {
	if c.TLSCAFile == "" && c.TLSCertFile == "" && c.TLSKeyFile == "" && !c.InsecureSkipVerify {
		return nil
	}
	tc := &client.TLSConfig{
		CAFile:   c.TLSCAFile,
		CertFile: c.TLSCertFile,
		KeyFile:  c.TLSKeyFile,
	}
	if c.InsecureSkipVerify {
		tc.Config = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式配置
	}
	return tc
}

// poolConfig 构建会话池配置。
//
// 注意:不设置 Database —— 客户端 Open 会把它作为会话数据库向服务端校验/切换,
// 库尚未创建时会直接失败;本后端一律用全限定路径,并用 CREATE DATABASE 显式建库。
// ConnectRetryMax=1:原生客户端的写调用没有 context 取消,断网时内部重连会逐次
// 无超时拨号,限制为 1 次以免单个 worker 长时间卡死、拖垮整个写入组。
func (c *Config) poolConfig() *client.PoolConfig {
	pc := &client.PoolConfig{
		UserName:        c.Username,
		Password:        c.Password,
		ConnectRetryMax: 1,
		TLSConfig:       c.tlsConfig(),
	}
	if len(c.Nodes) > 0 {
		pc.NodeUrls = append([]string(nil), c.Nodes...)
	} else {
		pc.Host = c.Host
		pc.Port = c.port()
	}
	return pc
}

// newPool 创建会话池(尚未真正连接,首条会话在 GetSession 时建立)。
// 池容量额外留 2 个槽位给补发协程与重连探测,避免与写入 worker 争抢导致取连接超时。
func (c *Config) newPool() sessionPool {
	p := client.NewSessionPool(c.poolConfig(), c.batchWorkers()+2, connectTimeoutMs, getSessionTimeoutMs, c.RPCCompression)
	return &iotdbSessionPool{pool: &p}
}

// portStr 兼容字符串/数字形式的端口配置
type portStr string

// UnmarshalJSON 同时接受 "6667" 与 6667 两种 JSON 类型
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
	return fmt.Errorf("iotdb: invalid port %s", string(b))
}

// nodeList 兼容 JSON 数组与英文逗号分隔字符串两种节点写法。
type nodeList []string

// UnmarshalJSON 接受 ["a:6667","b:6667"] 与 "a:6667,b:6667" 两种 JSON 类型。
func (n *nodeList) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*n = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("iotdb: invalid nodes %s", string(b))
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	*n = out
	return nil
}

// normalizeDatabasePath 规范化为 IoTDB 树模型数据库路径:
// 按 '.' 分段、逐段消毒,首段缺 root 时补齐;总长超过 64 字符直接报错
// (IoTDB 对数据库路径长度的硬限制,静默截断会写错库)。
func normalizeDatabasePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.Trim(raw, "."))
	var parts []string
	for _, p := range strings.Split(raw, ".") {
		if p == "" {
			continue
		}
		parts = append(parts, sanitizeIdent(p))
	}
	if len(parts) == 0 || !strings.EqualFold(parts[0], "root") {
		parts = append([]string{"root"}, parts...)
	} else {
		parts[0] = "root" // IoTDB 根节点固定小写
	}
	path := strings.Join(parts, ".")
	if len(path) > maxDatabaseLen {
		return "", fmt.Errorf("iotdb: database path %q exceeds %d characters", path, maxDatabaseLen)
	}
	return path, nil
}

// sanitizeIdent 将字符串消毒为合法路径标识符(仅保留 [A-Za-z0-9_],
// 非法字符替换为下划线;首字符为数字时补下划线前缀)。
// 与 tdengine 的 sanitizeIdent 语义一致,IoTDB 路径节点同样按此规则收敛。
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
