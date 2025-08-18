# 构建阶段
FROM golang:1.21-alpine AS builder

WORKDIR /app

# 安装依赖
RUN apk add --no-cache git

# 复制go mod文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建二进制文件
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o zombie-cleaner \
    ./main.go

# 运行阶段
FROM alpine:3.18

# 安装必要的工具
RUN apk add --no-cache \
    ca-certificates \
    procps \
    util-linux \
    && rm -rf /var/cache/apk/*

# 创建非root用户
RUN addgroup -g 1000 zombie && \
    adduser -D -u 1000 -G zombie zombie

WORKDIR /app

# 复制二进制文件
COPY --from=builder /app/zombie-cleaner .

# 创建配置目录
RUN mkdir -p /etc/zombie-cleaner && \
    chown -R zombie:zombie /app /etc/zombie-cleaner

# 暴露指标端口
EXPOSE 9090

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9090/health || exit 1

USER zombie

ENTRYPOINT ["./zombie-cleaner"]
CMD ["-config", "/etc/zombie-cleaner/config.yaml"]
