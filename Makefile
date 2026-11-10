# API Proxy Makefile

GREEN=\033[0;32m
YELLOW=\033[0;33m
NC=\033[0m

.PHONY: help dev run build check clean frontend-dev frontend-build embed-frontend

help:
	@echo "$(GREEN)API Proxy - 可用命令:$(NC)"
	@echo ""
	@echo "$(YELLOW)开发:$(NC)"
	@echo "  make dev            - 前端 Vite 与后端 Air 热重载开发"
	@echo "  make run            - 构建前端并运行 Go 后端"
	@echo "  make frontend-dev   - 前端开发服务器"
	@echo ""
	@echo "$(YELLOW)构建:$(NC)"
	@echo "  make build          - 构建前端并编译 Go 后端"
	@echo "  make frontend-build - 仅构建前端"
	@echo "  make clean          - 清理构建文件"
	@echo ""
	@echo "$(YELLOW)门禁:$(NC)"
	@echo "  make check          - 全量门禁（前后端检查与测试）"

dev:
	@echo "$(GREEN)🚀 启动前后端开发模式...$(NC)"
	@cd frontend && npm run dev &
	@cd backend-go && $(MAKE) dev

run: embed-frontend
	@cd backend-go && $(MAKE) run

build: embed-frontend
	@cd backend-go && $(MAKE) build

# 构建前端后，通过 scripts/copy-frontend.mjs 复制到 backend-go/frontend/dist。
# 复制脚本根据自身位置解析路径，避免因当前工作目录不同而产生嵌套目录。
embed-frontend:
	@echo "$(GREEN)📦 构建前端...$(NC)"
	@cd frontend && npm run build
	@echo "$(GREEN)📋 嵌入前端到 Go 后端...$(NC)"
	@node scripts/copy-frontend.mjs

clean:
	@cd backend-go && $(MAKE) clean
	@rm -rf frontend/dist

frontend-dev:
	@cd frontend && npm run dev

frontend-build:
	@cd frontend && npm run build

# 全量门禁统一走 check.mjs，与 npm run check 保持一致。
check:
	@node scripts/check.mjs
