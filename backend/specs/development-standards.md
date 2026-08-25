# 开发技术规范

## 1. 命名规范

### 1.1 Go 基础命名

| 元素 | 规范 | 示例 |
|------|------|------|
| 包名 | 小写，单数名词 | `user`, `configFile`, `database` |
| 文件名 | 小写，下划线/驼峰分隔 | `user_service.go`, `tsdbController.go` |
| 结构体 | 驼峰式，首字母大写 | `UserService`, `CreateUserDTO`, `TimeSeriesPoint` |
| 接口 | 以 `er` 结尾 或 功能命名 | `Reader`, `TimeSeriesDB`, `BaseRepository` |
| 变量 | 驼峰式，首字母小写 | `userService`, `accessToken`, `tsdb` |
| 常量 | 全大写，下划线分隔（Go 惯例）或 PascalCase | `LevelDebug`, `TokenExpiredErr` |
| 枚举值 | PascalCase | `UserExists`, `Breakfast`, `RegistrationGiftEnum` |

### 1.2 Wire ProviderSet 命名

| 元素 | 规范 | 示例 |
|------|------|------|
| ProviderSet 变量 | 以 `ProviderSet` 结尾 | `ServiceProviderSet`, `DatabaseProviderSet` |
| ProviderSet 文件 | `wire_set.go` | `service/wire_set.go` |
| 构造函数 | `New` 前缀 | `NewTSDBService`, `NewUserController` |
| Wire 入口函数 | `Initialize` 前缀 | `InitializeApp` |

### 1.3 数据库命名

| 元素 | 规范 | 示例 |
|------|------|------|
| 表名 | snake_case，复数 | `users`, `diet_records` |
| 列名 | snake_case | `user_id`, `mobile_phone_number`, `created_at` |
| 主键 | `id` | `id VARCHAR(32) PRIMARY KEY` |
| 时间字段 | `created_at`, `updated_at` | `created_at TIMESTAMP` |

### 1.4 JSON 字段命名

- 使用 **camelCase**
- 示例：`accessToken`, `isSuccess`, `serverTime`, `createdTime`

### 1.5 GORM 标签命名

- 数据库列使用 snake_case
- JSON 字段使用 camelCase
- Go 字段使用 PascalCase

```go
type User struct {
    ID        string    `gorm:"primaryKey;column:id"                   json:"id"`
    Mobile    string    `gorm:"column:mobile_phone_number"            json:"mobile"`
    CreatedAt time.Time `gorm:"column:created_at"                     json:"-"`
    CreatedAt string    `gorm:"-"                                     json:"createdTime"`
}
```

---

## 2. 文件组织规范

### 2.1 按领域实体分包

每个业务实体一组文件，放在对应层的独立文件中：

```
controller/userController.go
service/userService.go
model/po/user.go
model/dto/user_dto.go
model/vo/user_vo.go
router/userRouter.go
```

### 2.2 路由注册规范（RouteOption 模式）

- 每个领域实体一个路由文件（`xxxRouter.go`）
- 主 `router.go` 中定义 `RouteOption` 类型和 `SetupRouter`
- 每个模块提供 `WithXXXRoutes` 选项函数
- Wire 在 `ProvideRouteOptions` 中组装所有选项

```go
// userRouter.go
func UserRoutes(r *gin.Engine, uc *controller.UserController) {
    user := r.Group("/user")
    {
        user.POST("/register", uc.RegisterUser)
        user.POST("/login", uc.LoginUser)
    }
    authed := user.Group("", middleware.AuthMiddleware())
    {
        authed.GET("/profile", uc.GetUserProfile)
    }
}
```

```go
// router.go
type RouteOption func(*gin.Engine)

func SetupRouter(opts []RouteOption) *gin.Engine {
    r := gin.Default()
    r.Use(middleware.RequestLogMiddleware())
    r.Use(middleware.AreaMiddleware())
    r.GET("/health", healthCheck)

    for _, opt := range opts {
        opt(r)
    }
    return r
}

func WithUserRoutes(uc *controller.UserController) RouteOption {
    return func(r *gin.Engine) {
        UserRoutes(r, uc)
    }
}
```

---

## 3. 错误处理规范

### 3.1 错误类型体系

统一使用 `appError.AppError` 结构体：

```go
type AppError struct {
    Code    string // 错误码
    Message string // 默认消息（中文）
    MsgKey  string // i18n 翻译 key（空时直接使用 Message）
}
```

### 3.2 错误定义

- 包级变量定义常见错误
- 支持 i18n 翻译的通过 `NewAppErrorCtx` 创建

```go
var TokenExpiredErr = AppError{
    Code:    enums.TokenExpiredErr.GetCode(),
    Message: enums.TokenExpiredErr.GetMessage(),
    MsgKey:  "err.token_expired",
}
```

### 3.3 错误处理函数

```go
// 在 Controller 层统一处理
err := someService.SomeMethod(ctx, dto)
if err != nil {
    c.JSON(200, appError.HandleErrorCtx(ctx, err))
    return
}
```

`HandleErrorCtx` 内部执行三级匹配：
1. **`*AppError`** → 取业务错误码和翻译消息
2. **`validator.ValidationErrors`** → 返回参数校验错误
3. **其他错误** → 返回 `code: "-1", msg: "system error"`

### 3.4 最佳实践

- Controller 层**必须**处理所有返回的错误
- Service 层**必须**返回有意义的错误（而非 `fmt.Errorf`）
- Repository 层**统一处理** `gorm.ErrRecordNotFound` → 返回 nil（非错误）
- 不要吞掉错误日志——`HandleErrorCtx` 会自动记录

---

## 4. 日志规范

### 4.1 日志级别

| 级别 | 包级函数 | 使用场景 |
|------|----------|----------|
| DEBUG | `logger.Debug()` | 调试信息，仅开发环境 |
| INFO | `logger.Info()` | 正常操作信息 |
| WARN | `logger.Warn()` | 可恢复的异常情况 |
| ERROR | `logger.Error()` | 需要关注的错误 |
| FATAL | `logger.Fatal()` | 不可恢复的致命错误（会 exit） |

### 4.2 日志内容规范

- 保持简洁，关键信息前置
- 敏感信息（密码、令牌、身份证号）脱敏
- 使用结构化格式：`[级别] 调用者 消息`

```go
// 好的做法
logger.Info("创建用户成功, userId=%s", userId)
logger.Error("数据库连接失败, dsn=%s, err=%v", maskedDSN, err)

// 不好的做法
logger.Info("成功")
logger.Error(err.Error())
```

### 4.3 请求日志（中间件）

`RequestLogMiddleware` 自动录制：
- 请求方法、路径、IP、请求体
- 响应状态码、响应体（截断）
- 处理耗时
- 按状态码分级：500+ → ERROR, 400+ → WARN, 其他 → INFO

---

## 5. Response 响应规范

### 5.1 统一响应结构（Go 1.25+ 泛型）

```go
type Response[T any] struct {
    Code       string `json:"code"`       // "0" = 成功
    Message    string `json:"msg"`        // 描述信息
    IsSuccess  bool   `json:"isSuccess"`  // 成功标识
    Data       T      `json:"data"`       // 泛型数据
    ServerTime string `json:"serverTime"` // 服务器时间戳
}

func Success(data any) Response[any]    // 成功响应
func Fail(code, msg string) Response[any] // 失败响应
```

### 5.2 分页响应

```go
type PageVo[T any] struct {
    Page    int   `json:"page"`
    Size    int   `json:"size"`
    Total   int64 `json:"total"`
    Records []T   `json:"records"`
}
```

### 5.3 响应规则

- HTTP 状态码始终为 **200**，业务状态通过 `code` 字段传达
- `code: "0"` 表示成功，其他 code 均为失败
- 失败时 `data` 为 `null`
- 成功时 `msg` 为 "success"

---

## 6. 关系数据库操作规范

### 6.1 查询规范

```go
// WHERE 条件查询
db.Where("status = ?", "active").Find(&users)

// 关联查询使用 Preload
db.Preload("Profile").Find(&users)

// 分页查询
offset := (page - 1) * size
db.Limit(size).Offset(offset).Find(&users)

// 事务传播
func (r *BaseGormRepository) Create(ctx, tx, entity) {
    db := r.GenerateDB(tx)  // 有事务用事务，无事务用默认连接
    return db.WithContext(ctx).Create(entity).Error
}
```

### 6.2 事务规范

```go
// Service 层使用
err := dbManager.Transaction(ctx, func(tx *gorm.DB) error {
    // 所有操作使用 tx
    if err := repo.Create(ctx, tx, entity); err != nil {
        return err  // 自动回滚
    }
    return nil  // 自动提交
})

// 带重试的事务
err := dbManager.TransactionWithRetry(ctx, 3, func(tx *gorm.DB) error {
    // 业务操作
    return nil
})
```

### 6.3 Repository 方法清单

`BaseRepository` 接口提供 30+ 标准方法，包括：

| 类别 | 方法 |
|------|------|
| 基础 CRUD | `Create`, `Update`, `Delete`, `FindByID`, `FindOne`, `FindAll` |
| 分页 | `FindWithPage`, `FindLike` |
| 聚合 | `Count`, `Average`, `Averages`, `Exists` |
| 批量 | `BatchCreate`, `BatchUpdate`, `BulkUpsert` |
| 流式 | `StreamQuery`, `StreamQueryRaw`, `FindInBatches` |
| 连接 | `FindWithJoin`, `FindWithInnerJoin`, `FindWithLeftJoin`, `FindWithRightJoin`, `FindWithFullJoin` |
| 原生 SQL | `ExecuteRaw`, `ExecuteRawWithPage` |

---

## 7. 时序数据库操作规范（TimeSeriesDB）

### 7.1 统一数据点构造

```go
point := &database.TimeSeriesPoint{
    STable:      "meters",                        // 超级表名（TDEngine 可选）
    Measurement: "d1001",                         // 子表名 / measurement
    Timestamp:   time.Now(),                      // 时间戳
    Fields:      map[string]interface{}{          // 指标列
        "current": 10.2,
        "voltage": 291,
        "phase":   0.32,
    },
    Tags: map[string]string{                      // 标签（维度列）
        "location": "California.SanFrancisco",
        "group_id": "2",
    },
}
```

### 7.2 写入规范

```go
// 单点写入
err := tsdb.Write(ctx, point)

// 批量写入（相同 measurement）
err := tsdb.WriteMulti(ctx, points)
```

### 7.3 查询规范

```go
req := &database.QueryRequest{
    Measurement: "d1001",
    StartTime:   &start,
    EndTime:     &end,
    TagFilters:  map[string]string{"location": "California.*"},
    Fields:      []string{"current", "voltage"},
    Limit:       100,
    OrderBy:     "DESC",
}

result, err := tsdb.Query(ctx, req)
// result.Columns → []string
// result.Rows    → [][]interface{}
```

### 7.4 引擎选择

```yaml
# 通过配置切换引擎
tsdb:
  type: "tdengine"   # 或 "influxdb"

# 不配置时自动检测：
#   tdEngine.host 不为空 → tdengine
#   influxDB.host 不为空 → influxdb
#   都不配置 → 默认 tdengine + 打印警告
```

### 7.5 最佳实践

- 批量写入时确保所有 point 的 `Measurement` 相同
- 时间戳统一使用 `time.Now()`，不手动构造
- Tags 用于维度过滤，Fields 用于数值聚合分析
- 大量数据写入优先使用 `WriteMulti` 而非循环单点写入

---

## 8. 安全规范

### 8.1 输入验证

- Controller 层使用 Gin 的 `binding` 标签进行声明式验证
- 自定义验证规则在全局注册

```go
type CreateUserDTO struct {
    Email    string `json:"email"    binding:"required,email"`
    Password string `json:"password" binding:"required,min=6,max=32"`
    Age      int    `json:"age"      binding:"gte=0,lte=150"`
}
```

### 8.2 敏感数据处理

- 密码使用 `bcrypt` 哈希存储（`golang.org/x/crypto/bcrypt`）
- 敏感信息（密码、token、身份证）不记录日志
- 数据库连接信息通过环境变量注入，不硬编码
- 生产环境使用 SSL 连接数据库

### 8.3 API 安全

- JWT 令牌认证（accessToken 请求头）
- 路由按组控制权限（公共/需要认证）
- CORS 配置（gin 内置）
- 可选：请求频率限制、客户端密钥验证

---

## 9. 配置管理规范

### 9.1 配置层次

```
default.yaml  ← 通用的默认配置（已嵌入二进制，无需随包发布；运行目录存在同名文件时覆盖嵌入默认值）
dev.yaml      ← 开发环境覆盖（默认）
prod.yaml     ← 生产环境覆盖（通过 APP_ENV=prod 切换）
```

### 9.2 配置加载流程

1. `main.go` 先调用 `configFile.SetDefaultConfig(defaultYAML)` 注入嵌入的默认配置（`//go:embed default.yaml`）
2. 调用 `configFile.InitConfig()`
3. 读嵌入二进制的 `default.yaml` 设置默认值（单二进制部署无需外部配置文件）
4. 运行目录存在 `default.yaml` 时加载并合并（覆盖嵌入默认值，便于部署调参）
5. 根据 `APP_ENV` 环境变量加载 `dev.yaml` / `prod.yaml` 并合并覆盖
6. 反序列化为 `Config` 结构体
7. 通过返回值而非包级函数传给下层（Wire 注入）

### 9.3 配置结构设计

```go
type Config struct {
    Database DatabaseConfig `destructure:"database"`
    Server   ServerConfig   `destructure:"server"`
    Redis    RedisConfig    `destructure:"redis"`
    Jwt      JwtConfig      `destructure:"jwt"`
    Email    EmailConfig    `destructure:"email"`
    Log      LogConfig      `destructure:"log"`
    LLM      LLMConfig      `destructure:"llm"`
    TDEngine TDEngineConfig `destructure:"tdEngine"`
    InfluxDB InfluxDBConfig `destructure:"influxDB"`
    TSDB     TSDBConfig     `destructure:"tsdb"`
}
```

### 9.4 配置使用

```go
// main 中初始化后通过参数传递（推荐 Wire 注入）
cfg := configFile.InitConfig()

// 包内使用（线程安全）
config, _ := configFile.GetConfig()
dbHost := config.Database.Host
```

---

## 10. Google Wire 依赖注入规范

### 10.1 文件组织

```
wire.go         ← Wire 注入入口（//go:build wireinject）
wire_gen.go     ← Wire 自动生成（//go:build !wireinject）

database/wire_set.go    → DatabaseProviderSet
service/wire_set.go     → ServiceProviderSet
controller/wire_set.go  → ControllerProviderSet
cache/wire_set.go       → CacheProviderSet
```

### 10.2 ProviderSet 定义

```go
// service/wire_set.go
var ServiceProviderSet = wire.NewSet(
    NewTSDBService,
)
```

### 10.3 构造函数规范

所有通过 Wire 注入的类型必须提供公开构造函数（`New` 前缀）：

```go
func NewTSDBService(tsdb database.TimeSeriesDB) *TSDBService {
    return &TSDBService{tsdb: tsdb}
}

func NewTSDBController(tsdbService *service.TSDBService) *TSDBController {
    return &TSDBController{tsdbService: tsdbService}
}
```

### 10.4 ProviderSet 分层原则

- **DatabaseProviderSet**：数据库连接、Repository 实例
- **CacheProviderSet**：Redis 客户端
- **ServiceProviderSet**：业务逻辑，可依赖 DatabaseProviderSet
- **ControllerProviderSet**：HTTP 处理，可依赖 ServiceProviderSet

### 10.5 代码生成

```bash
go generate                          # 自动运行 wire
go run github.com/google/wire/cmd/wire  # 或手动运行
```

---

## 11. 测试规范

```bash
# 运行所有测试
go test ./...

# 带覆盖率报告
go test -cover ./...

# 特定包测试
go test ./service/...
```

- 单元测试与源代码放在同一包下
- 测试文件命名：`xxx_test.go`
- 使用 Go 标准 `testing` 包
- Mock 外部依赖（数据库、缓存、HTTP 客户端、时序数据库）

---

## 12. 代码风格

### 12.1 格式化

```bash
go fmt ./...
```

### 12.2 包导入顺序

```
标准库
空行
第三方包
空行
本地包
```

```go
import (
    "context"
    "errors"

    "github.com/gin-gonic/gin"
    "gorm.io/gorm"

    "iot-gateway/model/dto"
    "iot-gateway/service"
)
```

### 12.3 Error 处理原则

- 不忽略错误（不使用 `_` 忽略返回值）
- 错误尽早返回（fail fast）
- 错误链传递上下文信息
