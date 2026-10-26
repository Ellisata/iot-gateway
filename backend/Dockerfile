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

# 驱动版本真源：构建脚本、插件测试与 CI 共用这两个文件。
#   drivers.version  官方驱动仓库的 tag（go install 时使用）
#   drivers.txt      要打包的驱动目录名（每行一个）
COPY drivers.version drivers.txt ./

# 先复制模块清单,利用层缓存。SDK 与 testkit 都是已发布的 Go module，
# 由 GOPROXY 按 go.mod 的版本拉取。
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# 用第一阶段的前端产物替换 web/dist(web/dist 本身已被 .dockerignore 排除,不拷贝旧产物)
COPY --from=frontend /src/dist ./web/dist

# 网关版本: docker build --build-arg VERSION=v0.2.0 ...
# 注入 -X main.version 后二进制自描述;未注入时回退到 default.yaml 的 gateway.version。
ARG VERSION=dev

# CGO_ENABLED=0 禁用CGO,生成纯静态二进制(sqlite 用 glebarez 纯Go驱动)
# -ldflags="-s -w" 移除调试信息,减小二进制大小
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/iot-gateway .

# 外部协议驱动插件(进程外驱动): 按 drivers.txt + drivers.version 从模块代理拉取源码,
# 与主二进制同平台编译。
#
# **插件是运行镜像的必需组成**:全部协议驱动都由插件提供,主程序不含任何内置实现,
# plugins/ 为空 = 网关不支持任何协议。所以这里数一下编出几个,一个都没有就让构建失败
# —— 否则会产出一个能启动、能登录、却一台设备也采不到的镜像。
#
# 用临时模块解析源码：go install 在设置 GOBIN 时禁止交叉编译，而 go build 需要
# 一个主模块来解析依赖与 go.sum；-o 让产物名精确可控。
RUN mkdir -p /out/plugins /tmp/driverbuild && built=0 && \
    ver="$(tr -d '[:space:]' < /src/drivers.version)" && \
    [ -n "$ver" ] || { echo "[FAIL] drivers.version 为空"; exit 1; } && \
    expected="$(grep -cve '^[[:space:]]*$' /src/drivers.txt || true)" && \
    cd /tmp/driverbuild && GOWORK=off go mod init driverbuild >/dev/null 2>&1 && \
    while IFS= read -r name || [ -n "$name" ]; do \
        [ -n "$name" ] || continue; \
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go get \
            "github.com/Ellisata/iot-gateway-drivers/${name}@${ver}" >/dev/null || exit 1; \
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go build \
            -ldflags="-s -w -X main.version=${ver}" \
            -o "/out/plugins/iot-driver-${name}" \
            "github.com/Ellisata/iot-gateway-drivers/${name}" || exit 1; \
        echo "  [plugin] ${name} ${ver}"; \
        built=$((built + 1)); \
    done < /src/drivers.txt && \
    [ "$expected" -gt 0 ] && [ "$built" -eq "$expected" ] || \
    { echo "[FAIL] drivers.txt 列了 $expected 个插件，实际编出 $built 个"; exit 1; }

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
