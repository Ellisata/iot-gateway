# 第一阶段：构建阶段
#FROM golang:1.21-alpine AS builder
#
## 设置工作目录
#WORKDIR /app
#
## 复制go模块文件
#COPY go.mod go.sum ./
#RUN go mod download
#
## 复制源代码
#COPY . .
#
## 构建应用
## CGO_ENABLED=0 禁用CGO，生成纯静态二进制文件
## -ldflags="-s -w" 移除调试信息，减小二进制大小
#RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ibs .
#
# 第二阶段：运行阶段
FROM alpine:3.23.2
RUN apk --no-cache add poppler-utils tzdata

# 可选：如果需要字体支持（如 PDF 渲染更完整），添加字体包
#RUN apk --no-cache add ttf-freefont fontconfig
#
## 安装CA证书（如果需要HTTPS请求）
#RUN apk --no-cache add ca-certificates
#
## 创建非root用户运行应用
#RUN addgroup -g 1000 -S appuser && \
#    adduser -u 1000 -S appuser -G appuser

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
#COPY --from=builder /app/ibs .

COPY ./iot-hand-gateway .

RUN chmod +x iot-hand-gateway

# default.yaml 已通过 go:embed 嵌入二进制，单二进制部署无需额外文件。
# 如需在部署时覆盖嵌入的默认配置（如端口、JWT 密钥），取消下方 COPY 并放置自定义 default.yaml。
#COPY default.yaml ./default.yaml

# 切换用户
#USER appuser

# 暴露端口
EXPOSE 9081

# 健康检查
#HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  #CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# 运行应用
ENTRYPOINT ["/app/iot-hand-gateway"]