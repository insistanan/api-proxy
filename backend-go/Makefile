# Makefile for API Proxy Go Backend

# 变量定义
BINARY_NAME=api-proxy-go
MAIN_PATH=./cmd/server
BUILD_DIR=../dist
AIR_VERSION=v1.67.4
GOLANGCI_VERSION=v2.15.0
# 支持环境变量覆盖 VERSION（用于 Docker 构建时传入）
VERSION?=$(shell cat ../VERSION 2>/dev/null || echo "v0.0.0-dev")
BUILD_TIME=$(shell date '+%Y-%m-%d_%H:%M:%S_%Z')
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS=-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)

# 颜色输出
GREEN=\033[0;32m
YELLOW=\033[0;33m
NC=\033[0m # No Color

.PHONY: help dev build run clean install-air test check copy-frontend

# 默认目标 - 显示帮助
help:
	@echo "$(GREEN)API Proxy Go Backend - 可用命令:$(NC)"
	@echo ""
	@echo "$(YELLOW)开发命令:$(NC)"
	@echo "  make dev          - 启动开发模式 (热重载)"
	@echo "  make install-air  - 安装 Air 热重载工具"
	@echo ""
	@echo "$(YELLOW)构建命令:$(NC)"
	@echo "  make build        - 构建生产版本"
	@echo "  make run          - 直接运行 (自动复制前端)"
	@echo "  make copy-frontend- 复制前端资源"
	@echo "  make clean        - 清理构建文件"
	@echo ""
	@echo "$(YELLOW)测试命令:$(NC)"
	@echo "  make test         - 运行测试"
	@echo "  make test-cover   - 运行测试并生成覆盖率"
	@echo "  make check        - 门禁: gofmt 校验 + go vet + go test"

# 安装 Air 工具
install-air:
	@echo "$(GREEN)正在安装 Air 热重载工具...$(NC)"
	@go install github.com/air-verse/air@$(AIR_VERSION)
	@echo "$(GREEN)✅ Air 安装完成!$(NC)"
	@echo "使用方法: make dev"

# 开发模式 - 使用 Air 热重载
dev:
	@if ! command -v air &> /dev/null; then \
		echo "$(YELLOW)⚠️  Air 未安装，正在自动安装...$(NC)"; \
		$(MAKE) install-air; \
	fi
	@echo "$(GREEN)🚀 启动开发模式 (热重载开启)$(NC)"
	@echo "$(YELLOW)📝 监听文件变化: *.go, *.yaml, *.toml, *.env$(NC)"
	@echo "$(YELLOW)🔄 修改代码后将自动重启...$(NC)"
	@echo ""
	@air

# 开发模式 - 使用系统环境变量
dev-with-env:
	@echo "$(GREEN)🚀 启动开发模式 (使用系统环境变量)$(NC)"
	@ENV_MODE=development air

# 构建生产版本
build: copy-frontend
	@echo "$(GREEN)📦 构建生产版本...$(NC)"
	@CGO_ENABLED=0 go build -ldflags="$(LDFLAGS) -s -w" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✅ 构建完成: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

# 构建当前平台版本
build-local: copy-frontend
	@echo "$(GREEN)📦 构建本地版本...$(NC)"
	@go build -o $(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✅ 构建完成: ./$(BINARY_NAME)$(NC)"

# 直接运行 (带版本信息)
run: copy-frontend
	@echo "$(GREEN)▶️  直接运行...$(NC)"
	@go run -ldflags="$(LDFLAGS)" $(MAIN_PATH)

# 运行测试
test:
	@echo "$(GREEN)🧪 运行测试...$(NC)"
	@go test -v ./...

# 运行测试并生成覆盖率报告
test-cover:
	@echo "$(GREEN)🧪 运行测试并生成覆盖率...$(NC)"
	@go test -v -cover -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)✅ 覆盖率报告已生成: coverage.html$(NC)"

# 门禁检查: 提交前必跑（gofmt 校验 + go vet + go test）
check:
	@echo "$(GREEN)🔍 门禁检查: gofmt + vet + test$(NC)"
	@test -z "$$(gofmt -l .)" || { echo "$(YELLOW)❌ 以下文件未格式化，请先 make fmt:$(NC)"; gofmt -l .; exit 1; }
	@go vet ./...
	@go test ./...

# 清理构建文件
clean:
	@echo "$(YELLOW)🧹 清理构建文件...$(NC)"
	@rm -rf tmp/
	@rm -rf frontend/dist/
	@rm -f $(BINARY_NAME)
	@rm -f $(BUILD_DIR)/$(BINARY_NAME)
	@rm -f coverage.out coverage.html
	@rm -f build-errors.log
	@echo "$(GREEN)✅ 清理完成$(NC)"

# 通过统一脚本复制已有的前端产物；路径由脚本位置确定，不依赖当前工作目录。
copy-frontend:
	@echo "$(GREEN)📦 复制前端资源...$(NC)"
	@node ../scripts/copy-frontend.mjs

# 代码格式化
fmt:
	@echo "$(GREEN)🎨 格式化代码...$(NC)"
	@go fmt ./...
	@echo "$(GREEN)✅ 格式化完成$(NC)"

# 代码检查
lint:
	@echo "$(GREEN)🔍 检查代码...$(NC)"
	@if ! command -v golangci-lint &> /dev/null; then \
		echo "$(YELLOW)安装 golangci-lint...$(NC)"; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION); \
	fi
	@golangci-lint run

# 依赖管理
deps:
	@echo "$(GREEN)📚 更新依赖...$(NC)"
	@go mod tidy
	@go mod download
	@echo "$(GREEN)✅ 依赖更新完成$(NC)"

# 查看依赖树
deps-tree:
	@echo "$(GREEN)🌳 依赖树...$(NC)"
	@go mod graph

# 安装开发工具
install-tools: install-air
	@echo "$(GREEN)🔧 安装开发工具...$(NC)"
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@echo "$(GREEN)✅ 所有工具安装完成$(NC)"
