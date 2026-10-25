// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package configFile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"github.com/spf13/viper"
)

// EnvJwtSecret 环境变量名：JWT 签名密钥，优先级高于所有配置文件
const EnvJwtSecret = "IOT_GATEWAY_JWT_SECRET"

// EnvAdminPassword 环境变量名：初始 admin 账号口令，优先级高于所有配置文件
const EnvAdminPassword = "IOT_GATEWAY_ADMIN_PASSWORD"

// EnvGatewayName 环境变量名：网关名称，优先级高于所有配置文件。
// 容器 / 批量部署逐台注入名称时用它，比改配置文件省事。
const EnvGatewayName = "IOT_GATEWAY_NAME"

// EnvGatewayVersion 环境变量名：网关软件版本，优先级高于所有配置文件
const EnvGatewayVersion = "IOT_GATEWAY_VERSION"

// Config 全局配置结构体
//
// 每个字段的 mapstructure 标签是「yaml 键 → 字段」的显式映射，必须与 default.yaml
// 的顶层键逐一对应（Server↔server 这类名字恰好相同的，也照样写出来）。
// viper 的 Unmarshal 以 mapstructure 作 TagName；不写标签也能按「字段名不区分
// 大小写」匹配上，但那样字段一改名就变成静默失配 —— 配置被忽略、退回零值，
// 启动不报错，只有行为变得不对劲。显式写出来才能在 review 时看出来。
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Jwt       JwtConfig       `mapstructure:"jwt"`
	Auth      AuthConfig      `mapstructure:"auth"`
	Log       LogConfig       `mapstructure:"log"`
	SQLite    SQLiteConfig    `mapstructure:"sqlite"`
	Gateway   GatewayConfig   `mapstructure:"gateway"`
	Discovery DiscoveryConfig `mapstructure:"discovery"`

	// DeviceScan 局域网 PLC 扫描发现配置（网关主动扫本机网段找设备）
	DeviceScan DeviceScanConfig `mapstructure:"deviceScan"`

	// Push 推送引擎配置（采集数据投递到 MQTT / 时序库时的行为）
	Push PushConfig `mapstructure:"push"`

	// Plugin 外部协议插件（进程外驱动）配置
	Plugin PluginConfig `mapstructure:"plugin"`

	// AgentApi dsh（AI Agent）免鉴权调用面（/agentApi）配置
	AgentApi AgentApiConfig `mapstructure:"agentApi"`
}

// AgentApiConfig dsh（AI Agent）调用面 /agentApi 的配置。
//
// 该分组把全部领域路由整体镜像一遍，且**不校验任何凭据**（见
// middleware/authMiddleware.go 与 router/agentApiRouter.go 的说明）：能删设备、
// 上传并加载插件（等价于远程代码执行）、读取推送凭据。故出厂**关闭**，
// 与 discovery / plugin 等"接进来就能用"的开关方向相反。
type AgentApiConfig struct {
	// Enabled 总开关。出厂 false。
	//
	// 开启的前提只有两个：网关部署在隔离的工业内网，或 dsh 与网关同机。
	// 网关一旦暴露到不可信网络，这里必须保持关闭。
	//
	// **本项只是默认值**：运行期经过 runtimecfg.Manager 读取，可在「运行开关」页
	// 动态切换，**立即生效** —— 分组中间件按请求现读开关，关闭后下一次请求即被拒，
	// 不需要撤掉已注册的路由（gin 的路由表在启动后不可增删）。
	Enabled bool `mapstructure:"enabled"`
}

// PushConfig 推送引擎配置。
//
// 只管「投递行为」，通道自身的连接参数与缓存策略仍由 push_channel 表逐条维护
// —— 那些是运维在页面上改的，放进配置文件会让两处配置长期不一致。
type PushConfig struct {
	// ChangeOnly 变化才推送：点位值或质量码（quality）与上次**真正投递**的值相同
	// 时不再交给任何推送通道（MQTT / TDengine / InfluxDB）。
	//
	// 判断在 push.Engine 里做，不在采集侧：同一批采集记录还要喂给实时值快照，
	// 快照必须看到每一轮的值（否则页面上"最后更新时间"会停在值不变的那一刻），
	// 也避免把「下游去重」误做成「采集去重」。
	//
	// 没有「上次值」可比的点位总是投递：网关启动后首轮、以及新增/改配推送通道后
	// 的首轮都是如此 —— 否则稳定不变的点位会在新增通道上永远没有初值。
	// 只作用于本地点位采集；MQTT 订阅映射产生的记录不受影响（见 PushRecordsToTimeSeries）。
	//
	// 代价：值不变期间时序库里是空档而不是等间隔重复点，需要阶梯/前值填充的查询
	// 应由查询语句承担；依赖周期心跳数据的统计场景应关掉本项。
	//
	// **本项只是默认值**：运行期经过 runtimecfg.Manager 读取，可在「运行开关」页
	// 动态切换（下一批采集起生效，false -> true 时清空已投递记录再全量下发一次）。
	ChangeOnly bool `mapstructure:"changeOnly"`
}

// DeviceScanConfig 局域网 PLC 扫描发现配置。
//
// 与 DiscoveryConfig 是**反方向**的两件事，命名上刻意分开：
// DiscoveryConfig 让网关广播"我在哪"（供管理平台发现自己），本项让网关去问
// "网段里谁在"（扫出 PLC 并一键建成 device 行）。共用 discovery 这个词根会让
// 日志与文档里出现两个含义相反的 discovery。
//
// 扫描能力本身由协议插件声明（driversdk.Protocol.Discovery），这里只放
// 「跑一次扫描要花多少资源」这类与协议无关的旋钮。**刻意不含探测超时** ——
// 那是每协议的属性，由插件自报（见 driversdk.DiscoverySpec.MaxBudgetMs）。
type DeviceScanConfig struct {
	// Enabled 总开关。**出厂 true**，与 plugin.enabled / discovery.enabled 同一取舍：
	// 扫描是"接进来就能用"的能力，关掉它等于要求运维在图纸上逐台查 PLC 的 IP。
	//
	// 需要关掉的理由主要是合规：扫描会主动向整个网段发起连接，某些生产网络
	// 对此有明确的禁止条款。关闭后 /deviceScan/* 全部返回"功能未启用"。
	//
	// **本项只是默认值**：运行期经过 runtimecfg.Manager 读取，可在「运行开关」页
	// 动态切换。关闭只拦截新扫描，运行中的任务照常结束或由用户取消。
	Enabled bool `mapstructure:"enabled"`

	// MaxConcurrentProbes 同时在飞的目标探测数（默认 8，上限 32）。
	// 乘上单目标耗时就是扫一个网段的墙钟时间，同时也是插件进程一侧的并发压力：
	// 每个在飞探测在插件里占一个 goroutine 与一条到目标的连接。
	MaxConcurrentProbes int `mapstructure:"maxConcurrentProbes"`

	// MaxTargets 单次扫描的目标数上限（默认 1024，上限 65536）。
	//
	// 存在的意义是防手滑：一个 /16 展开是 65534 个目标，配上 1.5s 的拨号超时
	// 就是按小时计的扫描。**截断不是静默的** —— 任务快照里带 requestedTargets /
	// plannedTargets / truncated，前端必须把这个事实显示出来，否则用户会从
	// "没扫到设备"得出"这个网段没有设备"的错误结论。
	MaxTargets int `mapstructure:"maxTargets"`

	// JobTTLMinutes 扫描结束后任务与其结果的保留时长（默认 10 分钟）。
	//
	// 任务**不落库**：扫描是一次性运维动作，为它引入一张表与一次迁移不划算。
	// 代价是网关重启后未导入的结果会丢失 —— 写进 specs 与前端文案，不要让人猜。
	JobTTLMinutes int `mapstructure:"jobTTLMinutes"`
}

// GatewayConfig 网关自身身份配置。
//
// 两项的归属不同，别一概而论（见 database/sqlite/seedGateway.go 的字段归属表）：
// Name 只在首次启动（iot_gateway 表为空）时生效，行建好之后归运维
// （「系统设置」页维护）；Version 则**每次启动都同步进库**，改这里即时生效。
// 身份行的其余字段一律不被覆盖：它是运维数据，让一次重启静默回滚运维的编辑是
// 不可接受的（与 pluginhost 对 iot_protocol 行的取舍同源）。
type GatewayConfig struct {
	// Name 网关名称。留空时取主机名（os.Hostname），主机名也取不到则用 "IoT Gateway"。
	// 优先级更高的是环境变量 IOT_GATEWAY_NAME。
	Name string `mapstructure:"name"`

	// Version 网关软件版本号，随发行版更新；留空时回退为 "dev"。
	// 库内值以本项（或 IOT_GATEWAY_VERSION）为准：每次启动比对，不一致就刷新。
	// 优先级更高的是环境变量 IOT_GATEWAY_VERSION。
	Version string `mapstructure:"version"`
}

// DiscoveryConfig 局域网自动发现（UDP 广播）配置。
//
// 网关周期性把自身身份（id / 名称 / IP / 端口 / 固件版本 / 描述）以 UDP 广播发到
// 本机各网段的广播地址，并单播应答管理平台发来的探询。报文契约见 specs/discovery.md。
//
// **无认证**：收发两侧都不校验来源，同一局域网内任何主机都能看见、也能伪造。
// 这是发现阶段本就没有共享秘密的必然结果，不是疏漏 —— 代价是这条通道上
// **永远不得加入敏感字段**（当前只有身份信息，不含任何密钥）。
//
// 与 iot_gateway 身份行一样，各项都在**每次广播时实时生效**（不是只在首启读取）：
// 改端口 / 间隔重启后即可，改名称则无需重启（下一轮广播即带新值）。
type DiscoveryConfig struct {
	// Enabled 总开关。出厂默认 true（嵌入的 default.yaml 显式提供该键，
	// 见 InitConfig 的逐键合并 —— 磁盘配置省略整段时不会把它抹成 false）。
	// 关闭后网关对局域网完全静默，不影响采集与上报。
	//
	// **本项只是默认值**：运行期经过 runtimecfg.Manager 读取，可在「运行开关」页
	// 动态启停（Start / Stop UDP 套接字）。Port / IntervalMs 不在动态管理范围内。
	Enabled bool `mapstructure:"enabled"`

	// Port UDP 端口，默认 9083。广播与监听共用同一个端口。
	//
	// 同一台主机上跑第二个网关实例会因端口占用而启动失败（非致命，只记一条
	// ERROR，其余功能照常）——Go 对 UDP 不设 SO_REUSEADDR，这是刻意保留的行为：
	// 两个实例共用一个接收端口会让探询应答落到不确定的那个实例上。
	Port int `mapstructure:"port"`

	// IntervalMs 广播间隔（毫秒），默认 5000。启动时会额外立即广播一次，
	// 因此管理平台最坏等待时长等于本值，最好情况是一次报文内就发现。
	IntervalMs int `mapstructure:"intervalMs"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	// DefaultAdminPassword 初始 admin 账号口令，**仅在用户表为空（首次启动）时生效**。
	// 留空时优先读环境变量 IOT_GATEWAY_ADMIN_PASSWORD，仍为空则随机生成并打印到日志。
	// 账号建好之后本项不再被读取 —— 改密码请走登录后的修改密码接口，
	// 否则「配置里写了什么」与「库里是什么」会长期不一致。
	DefaultAdminPassword string `mapstructure:"defaultAdminPassword"`
}

// PluginConfig 外部协议插件配置。
//
// 注意：Enabled=true 等价于允许网关以自身 OS 权限执行 Dir 下的可执行文件。
// 该目录必须只对管理员可写，运行网关的账户不得对其有写权限。
type PluginConfig struct {
	// Enabled 总开关。默认 false —— 随发行版附带 plugins/ 目录不等于启用加载。
	//
	// **本项只是默认值**：可在「运行开关」页修改，但**重启网关后才生效**。
	// 插件管理器的 Stop 是终态的，运行期启停需要重写进程监管生命周期。
	Enabled bool `mapstructure:"enabled"`

	// Dir 插件目录。空或相对路径时相对**可执行文件所在目录**解析（不是 cwd）：
	// Windows 服务由 sc create 注册，SCM 给的工作目录是 System32。
	Dir string `mapstructure:"dir"`

	// Glob 插件文件名匹配。默认 "iot-driver-*"。
	Glob string `mapstructure:"glob"`

	// ScanIntervalMs 扫描间隔（毫秒），默认 10000。
	ScanIntervalMs int `mapstructure:"scanIntervalMs"`

	// HandshakeTimeoutMs describe 握手超时（毫秒），默认 10000。
	HandshakeTimeoutMs int `mapstructure:"handshakeTimeoutMs"`

	// ConnectTimeoutMs connect / ping 超时（毫秒），默认 30000。
	ConnectTimeoutMs int `mapstructure:"connectTimeoutMs"`

	// ReadTimeoutMs read 超时（毫秒），默认 60000。
	// 硬规则：必须大于 protocolJSON 里配置的协议自身超时，
	// 否则网关会放弃插件仍在执行的调用，重试会在串行总线上堆积。
	ReadTimeoutMs int `mapstructure:"readTimeoutMs"`

	// RestartMinDelayMs / RestartMaxDelayMs 崩溃重启退避区间（毫秒），默认 1000 / 30000。
	RestartMinDelayMs int `mapstructure:"restartMinDelayMs"`
	RestartMaxDelayMs int `mapstructure:"restartMaxDelayMs"`

	// MaxRestarts 连续失败次数上限，默认 10。超过后停止监管
	//（恢复需重启网关，或移除并重新放入插件文件）。
	MaxRestarts int `mapstructure:"maxRestarts"`

	// DisableProtocolRowProvisioning 关闭「依据插件 describe 自动维护
	// iot_protocol 行」的行为。字段名取反是为了让零值（配置缺省）等于默认行为：
	// 布尔字段在 yaml 缺省时反序列化为 false，若取名 EnableXxx 会被静默反转。
	DisableProtocolRowProvisioning bool `mapstructure:"disableProtocolRowProvisioning"`

	// AllowInsecureDir 允许插件目录为其他用户可写（不推荐）。
	// Unix 下默认拒绝世界可写目录；Windows 的 stat mode 无意义，只记警告。
	AllowInsecureDir bool `mapstructure:"allowInsecureDir"`

	// DisableHotUpgrade 关闭「运行期把已加载插件替换为磁盘上的新二进制」。
	//
	// 默认开启（同上一字段，字段名取反以让零值等于默认行为），理由是
	// Enabled=true 已经等于允许以网关权限执行该目录下的代码 —— 加载新版本
	// 与首次加载并无信任级别差异。
	//
	// 置 true 后回退到旧行为：检测到二进制被替换只记一条警告，继续跑旧版本，
	// 新版本在下次重启网关后生效。
	DisableHotUpgrade bool `mapstructure:"disableHotUpgrade"`
}

// SQLiteConfig SQLite 配置（与 Postgres 隔离，独立配置）
type SQLiteConfig struct {
	Path string `mapstructure:"path"` // 数据库文件路径，如 "data/iot.db"
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int `mapstructure:"port"`
}

// JwtConfig JWT 配置
type JwtConfig struct {
	Secret     string `mapstructure:"secret"`
	ExpireHour int    `mapstructure:"expireHour"`
	Issuer     string `mapstructure:"issuer"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level     string `mapstructure:"level"`
	OutputDir string `mapstructure:"outputDir"`
	MaxSizeMB int    `mapstructure:"maxSizeMB"` // 单日志文件大小上限（MB），超过则滚动当日下一个文件；<=0 不按大小滚动
	MaxDays   int    `mapstructure:"maxDays"`   // 日志文件最大保留天数，超过则删除旧文件；<=0 不清理
}

var (
	cfg    *Config
	rwLock sync.RWMutex
	once   sync.Once

	// defaultYAML 嵌入二进制的默认配置内容（由 main 包通过 go:embed default.yaml 注入）
	defaultYAML []byte
)

// SetDefaultConfig 注入嵌入的默认配置内容（main 包 go:embed default.yaml 提供），须在 InitConfig 前调用
func SetDefaultConfig(data []byte) {
	defaultYAML = data
}

// InitConfig 显式初始化配置（推荐在 main 中调用）
// 加载策略（优先级由低到高）：
//  1. 嵌入二进制的 default.yaml：基础配置，单二进制部署无需外部配置文件
//  2. 运行目录下的 default.yaml：若存在则加载并合并，覆盖嵌入默认值，便于部署调参
//  3. APP_ENV 指定的 dev.yaml / prod.yaml：若设置则加载并合并，环境配置覆盖默认值
func InitConfig() *Config {
	once.Do(func() {
		v := viper.New()
		v.SetConfigType("yaml")

		// 1. 加载嵌入的 default.yaml 作为基础配置
		if defaultYAML == nil {
			panic("embedded default config is nil, call configFile.SetDefaultConfig first")
		}
		if err := v.ReadConfig(bytes.NewReader(defaultYAML)); err != nil {
			panic("failed to read embedded default config: " + err.Error())
		}

		// 2. 运行目录存在 default.yaml 时加载并合并（覆盖嵌入默认值）
		if _, err := os.Stat("default.yaml"); err == nil {
			diskV := viper.New()
			diskV.SetConfigType("yaml")
			diskV.SetConfigName("default")
			diskV.AddConfigPath(".")
			if err := diskV.ReadInConfig(); err != nil {
				panic("failed to read external default config: " + err.Error())
			}
			for _, key := range diskV.AllKeys() {
				v.Set(key, diskV.Get(key))
			}
		}

		// 3. 根据 APP_ENV 加载 dev.yaml / prod.yaml 并合并（环境配置覆盖默认值）
		env := os.Getenv("APP_ENV")
		if env != "" {
			envV := viper.New()
			envV.SetConfigType("yaml")
			envV.AddConfigPath(".")
			envV.SetConfigName(env)
			if err := envV.ReadInConfig(); err != nil {
				panic("failed to read " + env + " config: " + err.Error())
			}
			for _, key := range envV.AllKeys() {
				v.Set(key, envV.Get(key))
			}
		}

		// 4. 反序列化到全局配置
		cfg = &Config{}
		if err := v.Unmarshal(cfg); err != nil {
			panic("failed to unmarshal config: " + err.Error())
		}

		// 5. JWT 密钥安全化：环境变量 > 配置文件值 > 随机生成
		cfg.Jwt.Secret = resolveJwtSecret(cfg.Jwt.Secret)
	})
	return cfg
}

// resolveJwtSecret 解析 JWT 签名密钥（优先级由高到低）：
//  1. 环境变量 IOT_GATEWAY_JWT_SECRET：容器 / 服务化部署注入密钥的首选方式
//  2. 配置文件中的非空 secret：部署方显式固定的密钥（重启后登录态保持）
//  3. 随机生成 32 字节十六进制密钥：未配置时的安全默认值，
//     每次启动重新生成（重启后所有登录态失效，但绝不会使用公开的固定密钥）
func resolveJwtSecret(configured string) string {
	if s := os.Getenv(EnvJwtSecret); s != "" {
		return s
	}
	if configured != "" {
		return configured
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("failed to generate random jwt secret: %v", err))
	}
	secret := hex.EncodeToString(buf)
	// 配置加载先于日志器初始化，且 logger 反向依赖本包（会循环导入），故直接输出到 stderr
	fmt.Fprintf(os.Stderr,
		"[WARN] jwt.secret not configured, generated an ephemeral secret: all sessions will be invalidated on restart. Set jwt.secret in config or the %s env var to persist sessions.\n", EnvJwtSecret)
	return secret
}

// GetConfig 线程安全读取配置
func GetConfig() (*Config, error) {
	rwLock.RLock()
	defer rwLock.RUnlock()
	if cfg == nil {
		return nil, nil
	}
	return cfg, nil
}

// MustGetConfig 读取失败则 panic
func MustGetConfig() *Config {
	cfg, err := GetConfig()
	if err != nil {
		panic("config not loaded: " + err.Error())
	}
	return cfg
}

// ReloadConfig 热重载配置
func ReloadConfig(dir, fileName string) error {
	rwLock.Lock()
	defer rwLock.Unlock()

	v := viper.New()
	v.SetConfigName(fileName)
	v.SetConfigType("yaml")
	if dir != "" {
		v.AddConfigPath(dir)
	}
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return err
	}

	newCfg := &Config{}
	if err := v.Unmarshal(newCfg); err != nil {
		return err
	}

	cfg = newCfg
	return nil
}
