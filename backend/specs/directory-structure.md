# 项目目录结构规范

## 1. 标准目录树

```
project-root/
│
├── main.go                    # 应用入口
├── go.mod                     # Go 模块定义
├── go.sum                     # 依赖校验文件
├── wire.go                    # Google Wire 注入入口（//go:build wireinject）
├── wire_gen.go                # Wire 自动生成的代码（//go:build !wireinject）
│
├── configFile/                # 配置管理
│   └── configFile.go          # 配置加载与结构体定义
│
├── router/                    # 路由层
│   ├── router.go              # 主路由（SetupRouter + RouteOption 定义）
│   ├── userRouter.go          # 按领域实体拆分
│   ├── tsdbRouter.go
│   └── ...
│
├── middleware/                # 中间件
│   ├── authMiddleware.go      # JWT 认证
│   ├── requestLogMiddleware.go # 请求日志
│   ├── areaMiddleware.go      # 区域设置
│   └── clientSecretMiddleware.go # 客户端密钥
│
├── controller/                # 控制器层（HTTP 处理）
│   ├── userController.go
│   ├── tsdbController.go
│   ├── wire_set.go            # Controller 层 Wire ProviderSet
│   └── ...
│
├── service/                   # 业务逻辑层
│   ├── userService.go
│   ├── tsdbService.go
│   ├── tsdbService_test.go    # 单元测试
│   ├── wire_set.go            # Service 层 Wire ProviderSet
│   └── ...
│
├── model/                     # 数据模型层
│   ├── dto/                   # 数据传输对象（请求体）
│   │   ├── user_dto.go
│   │   └── ...
│   ├── po/                    # 持久化对象（数据库表映射）
│   │   ├── user.go
│   │   └── ...
│   └── vo/                    # 视图对象（响应体）
│       ├── user_vo.go
│       ├── pageVo.go          # 通用分页 VO
│       └── ...
│
├── database/                  # 数据库操作层
│   ├── database.go            # 连接管理与单例
│   ├── crud.go                # BaseRepository 接口与实现
│   ├── transaction.go         # 事务管理
│   ├── batch.go               # 批量操作
│   ├── pagination.go          # 分页查询
│   ├── query.go               # 查询构建器
│   ├── errors.go              # 数据库错误处理
│   ├── wire_set.go            # 关系数据库 Wire ProviderSet
│   │
│   ├── tsdb.go                # TimeSeriesDB 统一接口 + 数据模型
│   ├── tsdb_influxdb.go       # InfluxDB 适配器
│   ├── tsdb_tdengine.go       # TDEngine 适配器
│   ├── influxdb.go            # InfluxDB v3 原生客户端
│   ├── tdengine.go            # TDEngine 原生客户端
│   └── tdengine_batch.go      # TDEngine 批量 Statement 模式
│
├── cache/                     # 缓存层
│   ├── redis.go               # Redis 封装（含分布式锁）
│   └── wire_set.go            # 缓存 Wire ProviderSet
│
├── logger/                    # 日志系统
│   └── log.go                 # 全局日志器
│
├── response/                  # 统一响应封装
│   └── response.go            # 泛型 Response[T]
│
├── appError/                  # 错误处理
│   ├── appError.go            # AppError 结构体
│   └── globalError.go         # HandleError 函数
│
├── enums/                     # 枚举定义
│   └── resultEnum.go          # 业务错误码枚举
│
├── i18n/                      # 国际化
│   ├── i18n.go                # 翻译引擎
│   ├── zh.json                # 简体中文
│   ├── en.json                # 英文
│   └── zh-hk.json             # 繁体中文（香港）
│
├── constants/                 # 常量定义
│   └── promptTemplateConstant.go # LLM 提示词模板
│
├── utils/                     # 工具函数
│   ├── jwtutil.go             # JWT 工具
│   ├── bcryptUtil.go          # 密码哈希
│   ├── uuidUtil.go            # UUID 生成
│   ├── contextUtil.go         # Context 提取
│   ├── httpClientUtil.go      # HTTP 客户端
│   ├── mapUtil.go             # Map 工具（泛型）
│   ├── dateUtil.go            # 日期计算
│   ├── jsonUtil.go            # JSON 工具
│   ├── md5Util.go             # MD5 哈希
│   ├── rsaUtil.go             # RSA 加解密
│   ├── uniqueSliceUtil.go     # 切片去重
│   ├── parseFileUtil.go       # 文件解析
│   └── clientSecretUtil.go    # 客户端密钥
│
├── llm/                       # LLM 集成
│   ├── modelClient.go         # LLMClient 接口定义
│   ├── llmModelInitLoad.go    # 模型配置加载
│   ├── deepseekClient.go      # DeepSeek 实现
│   └── openRouterClient.go    # OpenRouter 实现
│
├── mail/                      # 邮件服务
│   ├── smtpMail.go            # SMTP 邮件
│   └── zeptoMail.go           # ZeptoMail API
│
├── workerPool/                # 并发 Worker 池
│   └── workerPool.go
│
├── docs/                      # API 文档（swaggo 生成）
│   └── swagger.json
│
├── sql/                       # SQL 迁移脚本
│   └── init.sql
│
├── log/                       # 运行时日志文件（gitignore）
│
├── default.yaml               # 默认配置
├── dev.yaml                   # 开发环境配置
├── prod.yaml                  # 生产环境配置
│
├── redoc.html                 # ReDoc API 文档页面
│
├── Dockerfile                 # Docker 镜像构建
├── deploy.sh                  # 部署脚本
├── package-linux.bat          # Windows 交叉编译脚本
│
├── .gitignore
├── CLAUDE.md                  # 项目开发规范（Claude Code）
│
└── specs/                     # 可复用规范文档（本目录）
    ├── README.md
    ├── architecture.md
    ├── development-standards.md
    ├── technical-components.md
    └── directory-structure.md
```

---

## 2. 分包原则

### 2.1 按功能横向分层（Layer）

```
controller/  service/  model/  database/  router/
```

每一层对应一个架构层次，同层内的文件按领域实体拆分。

### 2.2 按领域纵向拆分（Domain）

同一领域的文件在同一层中以相同命名出现：

```
controller/userController.go
service/userService.go
model/dto/user_dto.go
model/po/user.go
model/vo/user_vo.go
router/userRouter.go
```

### 2.3 横切关注点独立成包

```
configFile/  # 配置
logger/      # 日志
cache/       # 缓存
i18n/        # 国际化
middleware/  # 中间件
```

这些包不依赖业务逻辑层，可以被任何层引用。

### 2.4 Model 层三分包

| 子包 | 职责 | 绑定 |
|------|------|------|
| `model/dto/` | 请求参数定义 | `json` + `binding` tag |
| `model/po/` | 数据库表映射 | `gorm` tag |
| `model/vo/` | 响应数据定义 | `json` tag |

### 2.5 Wire ProviderSet 文件分布

每层一个 `wire_set.go`，集中定义该层的 Wire ProviderSet：

```
controller/wire_set.go   → ControllerProviderSet
service/wire_set.go      → ServiceProviderSet
database/wire_set.go     → DatabaseProviderSet
cache/wire_set.go        → CacheProviderSet
```

---

## 3. 文件命名规范

| 层级 | 命名模式 | 示例 |
|------|----------|------|
| Controller | `{entity}Controller.go` | `userController.go` |
| Service | `{entity}Service.go` | `userService.go` |
| Router | `{entity}Router.go` | `userRouter.go` |
| PO | `{entity}.go` | `user.go` |
| DTO | `{entity}_dto.go` | `user_dto.go` |
| VO | `{entity}_vo.go` | `user_vo.go` |
| 单元测试 | `{entity}_test.go` | `tsdbService_test.go` |
| Wire ProviderSet | `wire_set.go` | `service/wire_set.go` |

---

## 4. 创建新功能模块的步骤

```bash
# 1. 定义数据模型
touch model/po/xxx.go          # PO（数据库映射）
touch model/dto/xxx_dto.go     # DTO（请求参数）
touch model/vo/xxx_vo.go       # VO（响应数据）

# 2. 实现业务逻辑（构造函数注入模式）
touch service/xxxService.go    # Service 层

# 3. 编写控制器
touch controller/xxxController.go  # Controller 层

# 4. 配置路由
touch router/xxxRouter.go      # 路由定义

# 5. 注册到 Wire ProviderSet
# 在对应的 wire_set.go 中追加 NewXxxService / NewXxxController

# 6. 注册路由选项
# 在 wire.go 的 ProvideRouteOptions 中追加 WithXxxRoutes

# 7. 运行 wire 重新生成
go generate

# 8. 添加单元测试
touch service/xxxService_test.go

# 9. 更新 API 文档
# 在 Controller 函数上添加 swaggo 注解
```

---

## 5. 各层文件模板

### Controller 模板（Wire 注入模式）

```go
package controller

import (
    "github.com/gin-gonic/gin"
    "iot-gateway/model/dto"
    "iot-gateway/appError"
    "iot-gateway/response"
    "iot-gateway/service"
)

type XxxController struct {
    xxxService *service.XxxService
}

func NewXxxController(xxxService *service.XxxService) *XxxController {
    return &XxxController{xxxService: xxxService}
}

func (c *XxxController) CreateXxx(ctx *gin.Context) {
    cCtx := ctx.Request.Context()
    var req dto.CreateXxxDTO
    if err := ctx.ShouldBindJSON(&req); err != nil {
        ctx.JSON(200, appError.HandleErrorCtx(cCtx, err))
        return
    }
    data, err := c.xxxService.CreateXxx(cCtx, &req)
    if err != nil {
        ctx.JSON(200, appError.HandleErrorCtx(cCtx, err))
        return
    }
    ctx.JSON(200, response.Success(data))
}
```

### Controller 模板（sync.Once 单例模式）

```go
package controller

import (
    "github.com/gin-gonic/gin"
    "iot-gateway/model/dto"
    "iot-gateway/appError"
    "iot-gateway/response"
    "iot-gateway/service"
)

var xxxService = service.GetXxxService()

func CreateXxx(c *gin.Context) {
    ctx := c.Request.Context()
    var req dto.CreateXxxDTO
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(200, appError.HandleErrorCtx(ctx, err))
        return
    }
    data, err := xxxService.CreateXxx(ctx, &req)
    if err != nil {
        c.JSON(200, appError.HandleErrorCtx(ctx, err))
        return
    }
    c.JSON(200, response.Success(data))
}

func GetXxx(c *gin.Context) {
    ctx := c.Request.Context()
    id := c.Param("id")
    data, err := xxxService.GetXxx(ctx, id)
    if err != nil {
        c.JSON(200, appError.HandleErrorCtx(ctx, err))
        return
    }
    c.JSON(200, response.Success(data))
}
```

### Service 模板（Wire 注入模式）

```go
package service

import (
    "context"
    "gorm.io/gorm"
    "iot-gateway/database"
    "iot-gateway/model/dto"
    "iot-gateway/model/vo"
    "iot-gateway/cache"
)

type XxxService struct {
    dm         *database.DBManager
    cache      *cache.RedisCache
    repository database.BaseRepository
}

func NewXxxService(
    dm *database.DBManager,
    cache *cache.RedisCache,
    repository database.BaseRepository,
) *XxxService {
    return &XxxService{
        dm:         dm,
        cache:      cache,
        repository: repository,
    }
}

func (s *XxxService) CreateXxx(ctx context.Context, dto *dto.CreateXxxDTO) (*vo.XxxVO, error) {
    var result *vo.XxxVO
    err := s.dm.Transaction(ctx, func(tx *gorm.DB) error {
        // 业务逻辑
        return nil
    })
    return result, err
}
```

### Service 模板（sync.Once 单例模式）

```go
package service

import (
    "context"
    "sync"
    "gorm.io/gorm"
    "iot-gateway/database"
    "iot-gateway/model/dto"
    "iot-gateway/model/vo"
    "iot-gateway/cache"
)

type XxxService struct {
    dm         *database.DBManager
    cache      *cache.RedisCache
    repository database.BaseRepository
}

var (
    xxxService *XxxService
    xxxOnce    sync.Once
)

func GetXxxService() *XxxService {
    xxxOnce.Do(func() {
        xxxService = &XxxService{
            dm:         database.GetDBManager(),
            cache:      cache.GetRedisCache(),
            repository: database.NewBaseGormRepository(),
        }
    })
    return xxxService
}

func (s *XxxService) CreateXxx(ctx context.Context, dto *dto.CreateXxxDTO) (*vo.XxxVO, error) {
    var result *vo.XxxVO
    err := s.dm.Transaction(ctx, func(tx *gorm.DB) error {
        // 业务逻辑
        return nil
    })
    return result, err
}
```

### Wire ProviderSet 模板

```go
// service/wire_set.go
package service

import "github.com/google/wire"

var ServiceProviderSet = wire.NewSet(
    NewXxxService,
    // 其他 Service 构造函数在此追加
)
```

### Wire 注入入口模板

```go
// wire.go
//go:build wireinject
// +build wireinject

package main

import (
    "github.com/gin-gonic/gin"
    "github.com/google/wire"
    "iot-gateway/configFile"
    "iot-gateway/controller"
    "iot-gateway/database"
    "iot-gateway/router"
    "iot-gateway/service"
)

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

func ProvideRouteOptions(
    tc *controller.TSDBController,
) []router.RouteOption {
    return []router.RouteOption{
        router.WithTSDBRoutes(tc),
    }
}
```

### Router 模板（RouteOption 模式）

```go
package router

import (
    "iot-gateway/controller"
    "github.com/gin-gonic/gin"
)

func XxxRoutes(r *gin.Engine, xc *controller.XxxController) {
    // 公共路由
    r.POST("/xxx", xc.CreateXxx)

    // 需要认证的路由
    authed := r.Group("", middleware.AuthMiddleware())
    {
        authed.GET("/xxx/:id", xc.GetXxx)
    }
}
```

### 主 Router 模板（SetupRouter + RouteOption）

```go
// router/router.go
package router

import (
    "github.com/gin-gonic/gin"
    "iot-gateway/controller"
    "iot-gateway/middleware"
)

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

func WithXxxRoutes(xc *controller.XxxController) RouteOption {
    return func(r *gin.Engine) {
        XxxRoutes(r, xc)
    }
}
```
