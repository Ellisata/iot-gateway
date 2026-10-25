# 多阶段构建: 前端构建 -> Go 编译 -> 运行镜像
# 构建上下文为仓库根目录(compose 中 context: .),需要 frontend/ 与 backend/ 源码。

# ========== 第一阶段: 前端构建 ==========
FROM node:22-alpine AS frontend

WORKDIR /src

# 先复制依赖清单,利用层缓存: package.json/lock 未变时跳过 npm ci
COPY frontend/package.json frontend/package-lock.json ./

# 如需国内镜像加速,取消注释:
#RUN npm config set registry https://registry.npmmirror.com

RUN npm ci

COPY frontend/ ./

# 产物输出到 /src/dist(vite 默认输出目录)
RUN npm run build

# ========== 第二阶段: Go 编译 ==========
FROM golang:1.26-alpine AS builder

# go mod download 直连时代仓库时可能需要 git
RUN apk --no-cache add git

WORKDIR /src

# 国内模块代理加速(直连 proxy.golang.org 会超时)
ENV GOPROXY=https://goproxy.cn,direct

# SDK 与官方驱动都是独立 Go module，通过 backend/ 下的 submodule 提供：
#   backend/sdk/        Apache-2.0 驱动 SDK
#   backend/driversrc/  AGPL-3.0 官方驱动
# backend/go.mod 用 replace ./sdk / ./driversrc 指向它们。先把两个 go.mod 复制进来，
# 让 go mod download 能解析本地 replace。
COPY backend/sdk/go.mod ./sdk/go.mod
COPY backend/driversrc/go.mod backend/driversrc/go.sum ./driversrc/

# 先复制模块清单,利用层缓存
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# 用第一阶段的前端产物替换 web/dist(web/dist 本身已被 .dockerignore 排除,不拷贝旧产物)
COPY --from=frontend /src/dist ./web/dist

# CGO_ENABLED=0 禁用CGO,生成纯静态二进制(sqlite 用 glebarez 纯Go驱动)
# -ldflags="-s -w" 移除调试信息,减小二进制大小
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/iot-gateway .

# 外部协议驱动插件(进程外驱动): 与主二进制同平台交叉编译。
#
# **插件是运行镜像的必需组成**:全部协议驱动都由插件提供,主程序不含任何内置实现,
# plugins/ 为空 = 网关不支持任何协议。所以这里数一下编出几个,一个都没有就让构建失败
# —— 否则会产出一个能启动、能登录、却一台设备也采不到的镜像。
# 目录布局是拍平的一层:driversrc/*/ 直接就是插件目录,产物名取目录 basename,
# 因此目录名必须唯一且不含层级。
RUN mkdir -p /out/plugins && built=0 && \
    cd driversrc && \
    for d in */; do \
        [ -f "${d}main.go" ] || continue; \
        name="$(basename "$d")"; \
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" \
            -o "/out/plugins/iot-driver-${name}" "./${name}" || exit 1; \
        echo "  [plugin] ${name}"; \
        built=$((built + 1)); \
    done && \
    [ "$built" -gt 0 ] || { echo "[FAIL] 没有编译出任何协议插件"; exit 1; }

# ========== 第三阶段: 运行镜像 ==========
FROM alpine:3.23.2
RUN apk --no-cache add poppler-utils tzdata

WORKDIR /app

COPY --from=builder /out/iot-gateway .
COPY --from=builder /out/plugins ./plugins

RUN chmod +x iot-gateway && chmod +x ./plugins/* 2>/dev/null || true

# default.yaml / i18n / migrations / web 前端均已 go:embed 嵌入二进制,单文件部署。
# 如需在部署时覆盖嵌入的默认配置(如端口、JWT 密钥),取消下方 COPY 并放置自定义 default.yaml。
# 注意: 配置项不支持环境变量(InitConfig 未开启 viper 的 AutomaticEnv),
# 所以容器里要改配置只能挂载一份 default.yaml。
#COPY default.yaml ./default.yaml

# plugins/ 是外部协议驱动插件目录,**镜像内已构建好全部协议插件、出厂即加载**
# (plugin.enabled 默认 true)。
# 不要用 `- ./plugins:/app/plugins:ro` 挂载空宿主目录 —— 那会把镜像内的插件
# **整个遮蔽**,网关将不支持任何协议。确需在运行期换插件时,先把发行包的
# plugins/ 内容拷进宿主目录再挂载,并以只读方式挂载避免插件改写自身二进制。

ENTRYPOINT ["/app/iot-gateway"]
