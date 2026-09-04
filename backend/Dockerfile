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

# 先复制模块清单,利用层缓存
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# 用第一阶段的前端产物替换 web/dist(web/dist 本身已被 .dockerignore 排除,不拷贝旧产物)
COPY --from=frontend /src/dist ./web/dist

# CGO_ENABLED=0 禁用CGO,生成纯静态二进制(sqlite 用 glebarez 纯Go驱动)
# -ldflags="-s -w" 移除调试信息,减小二进制大小
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/iot-gateway .

# ========== 第三阶段: 运行镜像 ==========
FROM alpine:3.23.2
RUN apk --no-cache add poppler-utils tzdata

WORKDIR /app

COPY --from=builder /out/iot-gateway .

RUN chmod +x iot-gateway

# default.yaml / i18n / migrations / web 前端均已 go:embed 嵌入二进制,单文件部署。
# 如需在部署时覆盖嵌入的默认配置(如端口、JWT 密钥),取消下方 COPY 并放置自定义 default.yaml。
#COPY default.yaml ./default.yaml

EXPOSE 9081

ENTRYPOINT ["/app/iot-gateway"]
