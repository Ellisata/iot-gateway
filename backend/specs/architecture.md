# 架构规范

## 1. 总体架构

采用**经典分层架构**，自顶向下分为五层，各层职责清晰、单向依赖：

```
main.go（应用入口）
   │
   ▼
router/（路由注册：URL → Controller 映射）
   │
   ▼
middleware/（横切关注点：认证、日志、区域）
   │
   ▼
controller/（HTTP 处理层：参数校验 + 响应封装）
   │
   ▼
service/（业务逻辑层：编排领域规则与外部依赖）
   │
   ├──► database/（数据访问层：GORM Repository 模式） ──► SQLite
   │
   └──► database/（时序数据库层：TimeSeriesDB 接口）  ──► TDEngine / InfluxDB
```

**核心原则**：
- **上层依赖下层**：Controller 调用 Service，Service 调用 Repository / TimeSeriesDB。
- **下层不依赖上层**：Repository 不知道 Controller 的存在。
- **同层可互相调用**：Service 层可以调用其他 Service。
- **横切模块**：配置、日志、缓存等横切关注点由全局模块管理，各层通过接口或函数调用使用。

---

## 2. 各层职责

### 2.1 Controller 层（controller/）

**职责**：
- 接收 HTTP 请求并解析参数（路径参数、查询参数、请求体）
- 使用 Gin 的 binding 标签进行参数校验
- 调用 Service 层执行业务逻辑
- 统一封装 JSON 响应（成功或错误）

**约束**：
- ❌ 不包含任何业务逻辑
- ❌ 不直接操作数据库
- ✅ 只做参数校验和响应封装

```go
// controller 标准模式
func CreateUser(c *gin.Context) {
    ctx := c.Request.Context()
    var dto dto.CreateUserDTO
    if err := c.ShouldBindJSON(&dto); err != nil {
        validationError(c, err)
        return
    }
    data, err := userService.CreateUser(ctx, &dto)
    if err != nil {
        c.JSON(200, appError.HandleErrorCtx(ctx, err))
        return
    }
    c.JSON(200, response.Success(data))
}
```

#### Wire 注入模式（推荐）

使用 Google Wire 编译期注入，Controller 通过结构体聚合依赖：

```go
type TSDBController struct {
    tsdbService *service.TSDBService
}

func NewTSDBController(tsdbService *service.TSDBService) *TSDBController {
    return &TSDBController{tsdbService: tsdbService}
}
```

### 2.2 Service 层（service/）

**职责**：
- 实现业务逻辑和领域规则
- 协调多个 Repository 或外部服务（缓存、时序数据库、LLM、邮件）
- 管理数据库事务
- 从 context 中提取用户身份和区域信息

**约束**：
- ✅ 可以调用其他 Service
- ✅ 可以操作缓存、时序数据库、发送邮件、调用 LLM
- ✅ 管理数据库事务
- ❌ 不直接处理 HTTP 请求/响应

```go
// service 标准模式（Wire 注入）
type TSDBService struct {
    tsdb database.TimeSeriesDB     // 统一时序数据库接口
}

func NewTSDBService(tsdb database.TimeSeriesDB) *TSDBService {
    return &TSDBService{tsdb: tsdb}
}
```

```go
// service 标准模式（sync.Once 单例）
type UserService struct {
    dm         *database.DBManager
    redisCache *cache.RedisCache
    repository database.BaseRepository
}

func (s *UserService) CreateUser(ctx context.Context, dto *dto.CreateUserDTO) (*vo.UserVO, error) {
    // 事务管理
    err := s.dm.Transaction(ctx, func(tx *gorm.DB) error {
        // 业务逻辑
        return nil
    })
    return userVO, err
}
```

### 2.3 Repository / Database 层（database/）

**职责**：
- 封装对关系数据库的所有操作（CRUD、分页、聚合查询）
- 提供事务传播机制
- 处理数据库连接池和查询构建

**约束**：
- ✅ 只处理数据持久化
- ✅ 支持事务传播（通过 `GenerateDB(tx)` 实现）
- ✅ 统一处理 `ErrRecordNotFound` 返回 nil
- ❌ 不包含业务逻辑

### 2.4 TimeSeriesDB 层（database/tsdb）

**职责**：
- 提供统一的时序数据库操作接口（`TimeSeriesDB`）
- 分别适配 TDEngine 和 InfluxDB v3
- 支持工厂模式自动检测引擎类型

```go
// 统一时序数据库接口
type TimeSeriesDB interface {
    Write(ctx context.Context, point *TimeSeriesPoint) error
    WriteMulti(ctx context.Context, points []*TimeSeriesPoint) error
    Query(ctx context.Context, req *QueryRequest) (*QueryResult, error)
    Ping(ctx context.Context) error
    Close() error
}

// 统一数据点模型
type TimeSeriesPoint struct {
    Measurement string
    STable      string                 // 超级表名（TDEngine 可选）
    Timestamp   time.Time
    Tags        map[string]string      // 标签（维度列）
    Fields      map[string]interface{} // 字段（指标列）
}
```

通过工厂函数自动选择引擎：

```go
tsdb, err := database.NewTimeSeriesDB(cfg)
// cfg.TSDB.Type = "tdengine" | "influxdb"
```

### 2.5 Model 层（model/）

数据契约分为三个子包：

| 子包 | 全称 | 职责 |
|------|------|------|
| `model/po/` | Persistence Object | 数据库表映射，GORM 模型定义 |
| `model/dto/` | Data Transfer Object | 请求参数绑定结构体 |
| `model/vo/` | View Object | 响应数据聚合结构体 |

**PO（持久化对象）**：
```go
type User struct {
    ID        string    `gorm:"primaryKey;column:id" json:"id"`
    Name      string    `gorm:"column:name" json:"name"`
    CreatedAt time.Time `gorm:"column:created_at" json:"-"`
    UpdatedAt time.Time `gorm:"column:updated_at" json:"-"`
}

func (User) TableName() string {
    return "users"
}
```

**DTO（数据传输对象）**：
```go
type CreateUserDTO struct {
    Name     string `json:"name"     binding:"required,min=2,max=50"`
    Email    string `json:"email"    binding:"required,email"`
    Password string `json:"password" binding:"required,min=6"`
}
```

**VO（视图对象）**：
```go
type UserVO struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    CreatedAt string `json:"createdAt"`
}
```

---

## 3. 依赖注入模式

项目采用**两阶段 DI 策略**：

### 3.1 编译期 DI：Google Wire（主应用入口）

```go
// wire.go
//go:build wireinject

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
```

Wire 自动生成 `wire_gen.go`，确保启动时依赖图完整，空指针错误在编译期暴露。

### 3.2 运行时 DI：sync.Once 单例（包内引用）

```go
var (
    userService *UserService
    once        sync.Once
)

func GetUserService() *UserService {
    once.Do(func() {
        userService = &UserService{
            dm:         database.GetDBManager(),
            cache:      cache.GetRedisCache(),
            repository: database.NewBaseGormRepository(),
        }
    })
    return userService
}
```

**Why this dual pattern**：
- **Wire**：应用入口的顶层依赖组装，保证启动时依赖完备
- **sync.Once**：Service/Repository 的包级按需加载，零外部依赖
- 两者可混用：Wire 也可以注入 `sync.Once` 单例中创建的实例

---

## 4. 路由注册模式

采用 **RouteOption 函数选项模式**，实现模块化路由注册：

```go
// router/router.go
func SetupRouter(opts []RouteOption) *gin.Engine {
    r := gin.Default()
    r.Use(middleware.RequestLogMiddleware())
    r.Use(middleware.AreaMiddleware())
    r.GET("/health", healthCheck)

    for _, opt := range opts {
        opt(r)  // 按需注册领域路由
    }
    return r
}

// 模块路由选项
func WithTSDBRoutes(tc *controller.TSDBController) RouteOption {
    return func(r *gin.Engine) {
        TSDBRoutes(r, tc)
    }
}

// Wire 中组装
func ProvideRouteOptions(tc *controller.TSDBController) []router.RouteOption {
    return []router.RouteOption{
        router.WithTSDBRoutes(tc),
    }
}
```

新增领域模块只需：
1. 创建 `xxxRouter.go` + `xxxController.go` + `xxxService.go`
2. 在 Wire ProviderSet 中追加
3. 在 `ProvideRouteOptions` 中追加 `WithXXXRoutes`

---

## 5. 数据流

```
HTTP Request
    │
    ▼
Middleware Chain ──► AuthMiddleware (JWT 验证)
    │                     │
    │                     ▼
    │               AreaMiddleware (区域设置)
    │                     │
    ▼                     ▼
Controller ──► DTO Binding ──► Validation
    │
    ▼
Service ──► Transaction ──► Context Propagation
    │                           │ (userId, area, tx)
    │
    ├──► Repository ──► GORM CRUD ──► SQLite
    │
    └──► TimeSeriesDB ──► Write / Query ──► TDEngine / InfluxDB
    │
    ▼
Response ──► Response[T] JSON
```

---

## 6. Wire ProviderSet 分层

每层定义自己的 ProviderSet，在 `wire_set.go` 中集中管理：

```
database/wire_set.go    →  DatabaseProviderSet
service/wire_set.go     →  ServiceProviderSet
controller/wire_set.go  →  ControllerProviderSet
cache/wire_set.go       →  CacheProviderSet
```

示例：

```go
// service/wire_set.go
var ServiceProviderSet = wire.NewSet(
    NewTSDBService,
    // 新增 Service 在此追加
)
```

---

## 7. 关键设计决策

### 7.1 统一响应格式

所有 API 返回统一的 JSON 结构，即使错误也使用 HTTP 200，通过业务 Code 区分：

```json
{
    "code": "0",
    "msg": "success",
    "isSuccess": true,
    "data": {},
    "serverTime": "2026-06-22 10:00:00"
}
```

### 7.2 Context 作为请求上下文载体

- `context.Context` 在 Service 层之间传递
- 存储 userId、area（区域）、clientSecret 等信息
- 通过 `context.WithValue` 注入，通过工具函数提取

### 7.3 事务传播

- Service 层使用 `DBManager.Transaction()` 包装事务
- Repository 通过 `GenerateDB(tx)` 方法选择使用事务连接或默认连接
- 支持带重试的事务执行

### 7.4 国际化（i18n）贯穿全栈

- `area` HTTP 请求头 → Context → Service → 错误翻译
- 错误枚举支持 `GetMessageCtx(ctx)` 方法
- 日志内容使用本地语言，翻译仅用于面向用户的响应

### 7.5 时序数据库统一抽象

- `TimeSeriesDB` 接口屏蔽 TDEngine 和 InfluxDB 的差异
- `TimeSeriesPoint` 统一数据模型，适配层负责转换
- 通过配置 `tsdb.type` 无感切换引擎
- 工厂函数 `NewTimeSeriesDB(cfg)` 自动检测或指定引擎

---

## 8. 时序数据库适配层架构

```
database/
├── tsdb.go              # TimeSeriesDB 接口 + 统一数据模型 + 工厂函数
├── tsdb_influxdb.go     # InfluxDB 适配器（impl TimeSeriesDB）
├── tsdb_tdengine.go     # TDEngine 适配器（impl TimeSeriesDB）
├── influxdb.go          # InfluxDB v3 原生客户端（底层）
├── tdengine.go          # TDEngine 原生客户端（底层）
└── tdengine_batch.go    # TDEngine 批量 Statement 模式
```

适配层工作流程：

```
应用代码 ──► TimeSeriesDB 接口 ──► 适配器 ──► 原生客户端 ──► 数据库

统一写入：
  TimeSeriesPoint{Measurement, STable, Timestamp, Tags, Fields}
      │
      ├──► InfluxDBAdapter ──► influxdb3.Point ──► WritePoints API
      │
      └──► TDEngineAdapter ──► INSERT SQL / STMT ──► WebSocket 连接
```

查询流程：

```
QueryRequest{Measurement, StartTime, EndTime, TagFilters, Fields, ...}
      │
      ├──► InfluxDBAdapter ──► SQL (InfluxDB v3 SQL dialect)
      │
      └──► TDEngineAdapter ──► SQL / Parameter Binding
```

这种设计使得上层业务代码完全不需要关心底层是哪个时序数据库，切换仅需修改配置。
