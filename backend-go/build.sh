#!/bin/bash

# API Proxy Go 版本构建脚本

set -e

# 版本信息 - 从根目录 VERSION 文件读取
VERSION=$(cat ../VERSION 2>/dev/null || echo "v0.0.0-dev")
BUILD_TIME=$(date '+%Y-%m-%d_%H:%M:%S_%Z')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# 构建标志
LDFLAGS="-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}"

echo "🚀 开始构建 API Proxy Go 版本..."
echo "📌 版本: ${VERSION}"
echo "🕐 构建时间: ${BUILD_TIME}"
echo "🔖 Git提交: ${GIT_COMMIT}"
echo ""

# 检查前端构建产物是否存在
if [ ! -d "../frontend/dist" ]; then
    echo "❌ 前端构建产物不存在，请先构建前端："
    echo "   cd ../frontend && npm run build"
    exit 1
fi

# 复制前端资源（统一走 scripts/copy-frontend.mjs，对 cwd 免疫，避免嵌套 dist 历史 bug）
echo "📦 复制前端资源..."
node ../scripts/copy-frontend.mjs

# 下载依赖
echo "📥 下载 Go 依赖..."
go mod download
go mod tidy

# 创建输出目录
mkdir -p dist

# 构建二进制文件
echo "🔨 构建二进制文件..."

# Linux
echo "  - 构建 Linux (amd64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o dist/api-proxy-linux-amd64 ./cmd/server

# Linux ARM64
echo "  - 构建 Linux (arm64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o dist/api-proxy-linux-arm64 ./cmd/server

# macOS
echo "  - 构建 macOS (amd64)..."
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o dist/api-proxy-darwin-amd64 ./cmd/server

# macOS ARM64 (M1/M2)
echo "  - 构建 macOS (arm64)..."
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "${LDFLAGS}" -o dist/api-proxy-darwin-arm64 ./cmd/server

# Windows
echo "  - 构建 Windows (amd64)..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "${LDFLAGS}" -o dist/api-proxy-windows-amd64.exe ./cmd/server

echo ""
echo "✅ 构建完成！"
echo ""
echo "📁 构建产物位于 dist/ 目录："
ls -lh dist/

echo ""
echo "💡 使用方法："
echo "  1. 复制对应平台的二进制文件到目标机器"
echo "  2. 创建 .env 文件配置环境变量"
echo "  3. 运行: ./api-proxy-linux-amd64"
echo ""
echo "📌 版本信息已注入到二进制文件中"

