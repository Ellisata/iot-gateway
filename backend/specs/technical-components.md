# 通用技术组件

本项目沉淀了以下可直接复用的技术组件，涵盖 Go Web 后端项目的常见需求。

---

## 1. 配置管理组件（configFile）

**核心文件**：`configFile/configFile.go`

### 功能特性

- 基于 Viper 的多环境配置加载
- 支持 `//go:embed` 嵌入配置到二进制文件（生产部署）
- 也支持从文件系统读取（开发调试）
- 线程安全的只读访问（`sync.RWMutex`）
- 支持热重载（`ReloadConfig`）
- 结构体映射（`destructure` 标签）

### 加载策略

1. `main.go` 先调用 `configFile.SetDefaultConfig(defaultYAML)` 注入嵌入的默认配置（`//go:embed default.yaml`）
2. `configFile.InitConfig()` 被调用（通常由 Wire 在 main 中触发）
3. 读嵌入二进制的 `default.yaml` 设默认值（单二进制部署无需外部配置文件）
4. 运行目录存在 `default.yaml` 时加载并合并（覆盖嵌入默认值，便于部署调参）
5. 根据 `APP_ENV` 环境变量加载 `dev.yaml` / `prod.yaml` 并合并覆盖
6. 反序列化为 `Config` 结构体

### 关键接口

```go
func InitConfig() *Config              // 初始化配置（推荐在 main 中调用）
func SetDefaultConfig(data []byte)     // 注入嵌入的 default.yaml（main 包 go:embed 提供，须在 InitConfig 前调用）
func GetConfig() (*Config, error)      // 线程安全读取
func MustGetConfig() *Config           // 读取失败则 panic
func ReloadConfig(dir, fileName) error // 热重载
```

### 配置结构设计

```go
type Config struct {
    Database DatabaseConfig  `destructure:"database"`
    Server   ServerConfig    `destructure:"server"`
    Redis    RedisConfig     `destructure:"redis"`
    Jwt      JwtConfig       `destructure:"jwt"`
    Email    EmailConfig     `destructure:"email"`
    Log      LogConfig       `destructure:"log"`
    LLM      LLMConfig       `destructure:"llm"`
    TDEngine TDEngineConfig  `destructure:"tdEngine"`
    InfluxDB InfluxDBConfig  `destructure:"influxDB"`
    TSDB     TSDBConfig      `destructure:"tsdb"`
}
```

### 配置 YAML 文件结构

```yaml
# default.yaml
server:
  port: 8080
sqlite:
  path: data/iot.db
tsdb:
  type: "tdengine"        # 时序数据库引擎选择
tdEngine:
  host: localhost
  port: 6041
  username: root
  password: taosdata
  dbName: iot_tsdb
influxDB:
  host: localhost
  port: 8086
  token: ""
  org: myorg
  dbName: iot_tsdb
```

---

## 2. 日志系统（logger）

**核心文件**：`logger/log.go`

### 功能特性

- 5 级日志：DEBUG / INFO / WARN / ERROR / FATAL
- 双重输出：控制台 + 文件（每日/按大小滚动，`errorTolerantWriter` 任一目标失败不影响其它）
- 滚动策略：按日期切换 `app-YYYYMMDD.log`，单文件超过 `maxSizeMB` 滚动为 `app-YYYYMMDD-N.log`
- 保留策略：后台维护协程周期性删除超过 `maxDays` 天的旧日志文件
- 自动调用者信息定位（栈回溯跳过日志框架自身）
- 包级函数全局可访问（`logger.Info()`）
- 初始化时自动读取配置中的日志级别、输出目录、大小上限与保留天数

### 关键接口

```go
func Debug(format string, v ...interface{})
func Info(format string, v ...interface{})
func Warn(format string, v ...interface{})
func Error(format string, v ...interface{})
func Fatal(format string, v ...interface{})
func Close()  // 关闭日志文件
```

### 输出格式

```
[INFO]  userService.go:42 创建用户成功, userId=abc123
[ERROR] database.go:78  数据库连接失败, err=...
```

### 日志文件命名

```
log/app-20260622.log      // 按日期滚动
log/app-20260622-1.log    // 当日单文件超过 maxSizeMB 后滚动
log/app-20260622-2.log
```

配置项（`log` 段）：

- `maxSizeMB`：单文件大小上限（MB），超过后滚动为当日下一个编号文件；`<=0` 不按大小滚动
- `maxDays`：日志保留天数，旧于该天数的文件在后台维护中被删除；`<=0` 不清理

---

## 3. 统一响应组件（response）

**核心文件**：`response/response.go`

### 功能特性

- 基于 Go 1.25+ 泛型的统一响应结构
- `Success` / `Fail` 工厂函数
- 自动注入服务器时间戳

### 接口定义

```go
type Response[T any] struct {
    Code       string `json:"code"`       // "0" = 成功
    Message    string `json:"msg"`        // 描述信息
    IsSuccess  bool   `json:"isSuccess"`  // 成功标识
    Data       T      `json:"data"`       // 泛型数据
    ServerTime string `json:"serverTime"` // 时间戳
}

func Success(data any) Response[any]   // code=0, isSuccess=true
func Fail(code, msg string) Response[any] // code=自定义, isSuccess=false
```

### 分页响应

```go
type PageVo[T any] struct {
    Page    int   `json:"page"`
    Size    int   `json:"size"`
    Total   int64 `json:"total"`
    Records []T   `json:"records"`
}
```

---

## 4. 错误处理组件（appError）

**核心文件**：`appError/appError.go`、`appError/globalError.go`

### 功能特性

- 统一业务错误码体系
- 支持 i18n 翻译（通过 MsgKey）
- 集成 Gin 的 ValidationErrors 处理
- 兜底保护（未识别错误返回通用系统错误）

### 关键接口

```go
type AppError struct {
    Code    string // 错误码
    Message string // 默认消息
    MsgKey  string // i18n 翻译 key
}

// 创建错误
func NewAppError(code, msg string) *AppError
func NewAppErrorCtx(code, msg, msgKey string) *AppError

// 处理错误（Controller 层使用）
func HandleError(err error) Response[any]         // 返回中文消息
func HandleErrorCtx(ctx, err) Response[any]        // 返回翻译消息
```

### 错误处理流程

```
Controller 收到 error
    │
    ▼
HandleErrorCtx(ctx, err)
    │
    ├─ errors.As → *AppError     ──► response.Fail(code, translatedMsg)
    ├─ errors.As → ValidationErrors ──► response.Fail(paramValidCode, field+tag)
    └─ 其他错误                   ──► response.Fail("-1", "system error")
```

---

## 5. 关系数据库操作组件（database）

**核心文件**：`database/database.go`、`database/crud.go`、`database/transaction.go`、`database/batch.go`

### 功能特性

- 基于 GORM 的单例连接管理
- `BaseRepository` 接口（30+ 标准方法）
- 事务管理（支持重试）
- 批量操作（批量创建、批量更新、Upsert）
- 流式查询（逐行处理大数据集）
- 连接池配置
- 多表连接查询（INNER/LEFT/RIGHT/FULL JOIN）
- 统一的数据库错误处理（`database/errors.go`）

### 连接池配置

```go
sqlDB.SetMaxIdleConns(10)
sqlDB.SetMaxOpenConns(100)
sqlDB.SetConnMaxLifetime(time.Hour)
sqlDB.SetConnMaxIdleTime(30 * time.Minute)
// 生产环境关闭 SQL 日志
// 预编译语句（PrepareStmt: true）
// 跳过默认事务（SkipDefaultTransaction: true）
```

### 事务传播模式

`GenerateDB(tx)` 是事务传播的核心模式：

```go
func (r *BaseGormRepository) GenerateDB(tx *gorm.DB) *gorm.DB {
    if tx == nil {
        return r.db    // 无事务，使用默认连接
    }
    return tx          // 有事务，使用事务连接
}
```

### 批量操作

```go
// 批量创建
func BatchCreate(db *gorm.DB, entities []interface{}, batchSize int) error

// 批量更新
func BatchUpdate(db *gorm.DB, entities []interface{}, column string, batchSize int) error

// Upsert（存在则更新，不存在则插入）
func BulkUpsert(db *gorm.DB, entities []interface{}, conflictColumns []string, updateColumns []string) error
```

---

## 6. 时序数据库组件（TimeSeriesDB）

**核心文件**：`database/tsdb.go`、`database/tsdb_influxdb.go`、`database/tsdb_tdengine.go`

### 功能特性

- 统一的时序数据操作接口，屏蔽底层引擎差异
- 支持 TDEngine（通过 WebSocket + STMT 协议）
- 支持 InfluxDB v3（通过 InfluxDB v3 Go SDK）
- 工厂模式自动检测引擎（基于配置或环境）
- 统一的数据模型（`TimeSeriesPoint`、`QueryRequest`、`QueryResult`）

### 架构层次

```
┌─────────────────────────────────────────────────┐
│                  应用代码                        │
│     tsdb.Write(ctx, point) / tsdb.Query(...)    │
└──────────────────────┬──────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────┐
│              TimeSeriesDB 接口                    │
│     database/tsdb.go（统一抽象层）                 │
└──────┬─────────────────────────────┬─────────────┘
       │                             │
┌──────▼──────────┐       ┌─────────▼────────────┐
│ InfluxDBAdapter │       │ TDEngineAdapter      │
│ tsdb_influxdb.go│       │ tsdb_tdengine.go     │
└──────┬──────────┘       └─────────┬────────────┘
       │                            │
┌──────▼──────────┐       ┌─────────▼────────────┐
│ InfluxDB v3    │       │ TDEngine Driver       │
│ influxdb3-go   │       │ taosWS / STMT         │
└─────────────────┘       └──────────────────────┘
```

### 统一数据模型

```go
type TimeSeriesPoint struct {
    Measurement string                 // 表名 / measurement
    STable      string                 // 超级表名（TDEngine 可选）
    Timestamp   time.Time              // 时间戳
    Tags        map[string]string      // 标签（维度列）
    Fields      map[string]interface{} // 字段（指标列）
}

type QueryRequest struct {
    Measurement string
    StartTime   *time.Time
    EndTime     *time.Time
    TagFilters  map[string]string
    Fields      []string
    Limit       int
    OrderBy     string            // "ASC" / "DESC"
    RawSQL      string            // 原生 SQL 直传
}

type QueryResult struct {
    Columns []string
    Rows    [][]interface{}
    Cost    time.Duration
}
```

### 统一接口

```go
type TimeSeriesDB interface {
    Write(ctx context.Context, point *TimeSeriesPoint) error
    WriteMulti(ctx context.Context, points []*TimeSeriesPoint) error
    Query(ctx context.Context, req *QueryRequest) (*QueryResult, error)
    Ping(ctx context.Context) error
    Close() error
}
```

### 工厂函数

```go
// 根据配置自动选择引擎
tsdb, err := database.NewTimeSeriesDB(cfg)

// 或带 Fatal 退出的版本（用于 Wire 注入）
tsdb := database.MustNewTimeSeriesDB(cfg)
```

### 引擎自动检测逻辑

```go
// 1. 显式配置 tsdb.type → "tdengine" / "influxdb"
// 2. 未配置时自动检测：
//    tdEngine.host != "" → tdengine
//    influxDB.host != "" → influxdb
// 3. 都未配置 → 默认 tdengine + 警告
```

---

## 7. 缓存组件（cache/redis）

**核心文件**：`cache/redis.go`

### 功能特性

- 基于 go-redis 的泛型封装
- `Set[T]` / `Get[T]` / 基本 Redis 操作
- 分布式锁（支持自动续期、Lua 脚本原子操作）

---

## 8. 国际化组件（i18n）

**核心文件**：`i18n/i18n.go`

### 功能特性

- JSON 文件驱动，支持 embed.FS 打包
- 基于 `area` HTTP 请求头的区域识别
- 区域到语言代码的自动映射
- 支持格式化消息（`Tf`, `TCtxf`）
- 中文作为默认回退语言

### 语言文件结构

```json
// zh.json
{
    "err.token_expired": "令牌已过期",
    "err.token_invalid": "无效的令牌"
}

// en.json
{
    "err.token_expired": "Token expired",
    "err.token_invalid": "Invalid token"
}
```

### 关键接口

```go
func T(lang, key string) string              // 根据语言代码翻译
func Tf(lang, key string, args ...any) string // 翻译并格式化
func TCtx(ctx, key string) string             // 从 context 获取语言并翻译
func TCtxf(ctx, key, args ...any) string      // 翻译 + 格式化 + Context
```

### 区域→语言映射

```go
"zh_HK", "tw", "mo" → "zh-hk"
"en", "us"          → "en"
其他                → "zh"
```

---

## 9. 认证组件（JWT）

**核心文件**：`utils/jwtutil.go`、`middleware/authMiddleware.go`

### 功能特性

- 基于 `golang-jwt/jwt/v5` 的令牌管理
- 用户 ID 存储在 `jwt.RegisteredClaims.ID` 字段
- 令牌验证、用户 ID 提取
- 通过中间件自动注入到 `context.Context`

### 中间件使用

```go
// 私有路由组
authed := r.Group("", middleware.AuthMiddleware())
{
    authed.GET("/profile", controller.GetProfile)
}
```

### Context 提取

```go
userId := c.GetString("userId") // Gin context
ctx = context.WithValue(ctx, "userId", userId) // 注入
```

---

## 10. 邮件服务组件（mail）

### 支持两种方式

1. **SMTP**：基于 `gomail` 库，传统邮件发送
2. **ZeptoMail API**：HTTP API 方式

### 功能

- HTML 模板渲染
- 验证码发送（验证码存储在 Redis）
- i18n 支持

---

## 11. LLM 集成组件（llm）

**核心文件**：`llm/modelClient.go`、`llm/deepseekClient.go`、`llm/openRouterClient.go`、`llm/llmModelInitLoad.go`

### 功能特性

- 统一的 LLM 客户端接口
- 支持 DeepSeek（流式/非流式）和 OpenRouter
- 思考模式（深度推理）和非思考模式切换
- 模型配置从数据库动态加载
- 使用 `fastjson` 构建请求 JSON（高性能）

### 统一接口

```go
type LLMClient interface {
    Chat(ctx context.Context, messages []LLMMessage) (*LLMResponse, error)
    ChatStream(ctx context.Context, messages []LLMMessage, onData func([]byte)) error
}

type LLMMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type LLMResponse struct {
    Content   string `json:"content"`
    Reasoning string `json:"reasoning,omitempty"`
}
```

### 架构

```
llm/
├── modelClient.go          # LLMClient 接口定义
├── llmModelInitLoad.go     # 模型配置动态加载（从数据库）
├── deepseekClient.go       # DeepSeek API 实现
└── openRouterClient.go     # OpenRouter API 实现
```

### 模型初始化

```go
// main.go 中启动时调用
llm.InitModels()

// 内部逻辑：
// 1. 从数据库加载模型配置列表
// 2. 根据配置创建对应的 LLMClient 实例
// 3. 注册到全局模型注册表
```

---

## 12. Worker 池组件（workerPool）

**核心文件**：`workerPool/workerPool.go`

### 功能特性

- 固定大小 goroutine 池
- 有缓冲任务通道
- 支持阻塞提交和非阻塞尝试
- 优雅关闭（等待所有任务完成）

### 使用方式

```go
pool := workerPool.NewWorkerPool(3, 10)
pool.Start()

// 阻塞提交
pool.Submit(func() {
    // 耗时任务
})

// 非阻塞尝试
if pool.TrySubmit(task) {
    // 提交成功
} else {
    // 通道满，任务被拒绝
}

pool.Stop() // 等待所有任务完成
```

---

## 13. 枚举组件（enums）

**核心文件**：`enums/`

### 功能特性

- 业务枚举统一管理
- 每个枚举值包含 `code`, `message`, `msgKey`
- 支持 i18n 翻译上下文

```go
type BusinessEnum struct {
    code    string
    message string
    msgKey  string
}

func (e *BusinessEnum) GetCode() string
func (e *BusinessEnum) GetMessage() string
func (e *BusinessEnum) GetMessageCtx(ctx context.Context) string
```

---

## 14. 工具函数集合（utils）

| 文件 | 功能 | 关键函数 |
|------|------|----------|
| `jwtutil.go` | JWT 令牌生成/验证 | `GenerateToken`, `ValidateToken`, `GetUserId` |
| `contextUtil.go` | Context 提取 | `GetUserIdFromCtx`, `GetAreaFromCtx` |
| `bcryptUtil.go` | 密码哈希 | `HashPassword`, `CheckPassword` |
| `rsaUtil.go` | RSA 加密/解密 | 客户端-服务端敏感传输 |
| `uuidUtil.go` | UUID 生成 | 无横线的 32 位 UUID |
| `httpClientUtil.go` | HTTP 客户端 | `HttpPost`, `HttpGet`（返回 fastjson.Value） |
| `dateUtil.go` | 日期计算 | 上周/月计算，日期范围生成 |
| `mapUtil.go` | Map 工具 | `GroupBy[T]`, `GroupByList[T]`（泛型） |
| `jsonUtil.go` | JSON 工具 | `PrettyPrint` |
| `md5Util.go` | MD5 哈希 | `MD5` |
| `uniqueSliceUtil.go` | 切片去重 | `UniqueSlice`（泛型） |
| `parseFileUtil.go` | 文件解析 | DOCX/PDF 解析 |
| `clientSecretUtil.go` | 客户端密钥 | 校验调度端身份 |

---

## 15. Google Wire 依赖注入组件

**核心文件**：`wire.go`、`wire_gen.go`

### 功能特性

- 编译期依赖注入，启动时即可发现缺失依赖
- 自动生成 `wire_gen.go`，无需手动维护组装代码
- 与分层架构天然适配
- 减少全局变量和 init 函数的使用

### 使用方式

```go
// 1. 定义 ProviderSet（每层一个 wire_set.go）
var ServiceProviderSet = wire.NewSet(
    NewTSDBService,
    NewUserService,
)

// 2. Wire 入口
// wire.go
func InitializeApp() (*gin.Engine, func()) {
    wire.Build(
        configFile.InitConfig,
        database.DatabaseProviderSet,
        service.ServiceProviderSet,
        controller.ControllerProviderSet,
        ProvideRouteOptions,
        router.SetupRouter,
    )
    return nil, nil
}

// 3. 生成 wire_gen.go
// go:generate go run github.com/google/wire/cmd/wire
```

### 项目接入步骤

1. 新建 `wire.go`（带 `//go:build wireinject` 编译标签）
2. 每层添加 `wire_set.go` 定义 ProviderSet
3. 所有注入类型提供 `New` 前缀构造函数
4. 在 `main.go` 中调用 `InitializeApp()` 替换手动组装
5. 运行 `go generate` 生成 `wire_gen.go`