# 阶段 1：准备 Node 24 和 npm，供前端构建及产物复制脚本使用。
# Node 与 Go 镜像使用相同的 Alpine 版本，避免运行库不兼容。
FROM node:24.16.0-alpine3.23 AS node-runtime

# 阶段 2：在 Go 镜像中完成前后端构建。
FROM golang:1.26.10-alpine3.23 AS builder

# 可用 --build-arg VERSION 覆盖版本号；未指定时由 Makefile 读取 VERSION 文件。
ARG VERSION

WORKDIR /src

# 安装 Makefile 所需工具，以及 Node 和前端原生依赖使用的运行库。
RUN apk add --no-cache git make libstdc++ libgcc

# 一并复制 Node、npm 包和命令链接，避免只复制可执行文件后找不到 npm。
COPY --from=node-runtime /usr/local/ /usr/local/

# 先安装锁文件指定的依赖；只修改源码时可以复用这一层缓存。
COPY frontend/package.json frontend/package-lock.json ./frontend/
RUN cd frontend && npm ci
COPY backend-go/go.mod backend-go/go.sum ./backend-go/
RUN cd backend-go && go mod download

# 复制源码和构建脚本，本机依赖、产物及运行配置由 .dockerignore 排除。
COPY Makefile VERSION ./
COPY scripts/ ./scripts/
COPY frontend/ ./frontend/
COPY backend-go/ ./backend-go/

# Makefile 负责构建前端、调用 copy-frontend.mjs，再编译嵌入资源的 Go 程序。
RUN if [ -n "${VERSION}" ]; then VERSION=${VERSION} make build; else make build; fi

# 阶段 3：运行镜像只保留程序、证书和时区数据。
FROM alpine:3.23 AS runtime

WORKDIR /app

# 安装运行时依赖
RUN apk --no-cache add ca-certificates tzdata

# 从构建阶段复制 Go 二进制文件（已内嵌前端资源）
COPY --from=builder /src/dist/api-proxy-go /app/api-proxy

# 配置和默认日志库保存在 /app/.config；/app/logs 保留给旧版挂载或自定义日志路径。
RUN mkdir -p /app/.config/backups /app/logs

# 默认使用上海时区，可在启动容器时覆盖 TZ。
ENV TZ=Asia/Shanghai

# 默认监听 3000，同时提供代理 API 与管理界面。
EXPOSE 3000

# 通过公开的 /health 检查服务是否可用。
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:3000/health || exit 1

# 启动命令
CMD ["/app/api-proxy"]
