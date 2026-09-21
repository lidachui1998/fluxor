# ============================================================================
# Fluxor 构建
# ----------------------------------------------------------------------------
#   make                 清理旧 dist → 构建前端 → 同步 → 编译二进制
#   make V=2.3.4         同上，并把版本号 2.3.4 注入二进制
#
# 版本号通过 -ldflags 在编译期写入 internal/buildinfo.Version；
# 前端不再注入版本，改由后端 GET /app-version 提供。
#
# 交叉编译交由 Go 自身处理：需要其它平台时设置 GOOS / GOARCH 环境变量即可，
# 例如 `make GOOS=linux GOARCH=arm64`。
# ============================================================================

BIN := fluxor
GO  := go
NPM := npm
export PATH := /var/apps/nodejs_v24/target/bin:$(PATH)

# 版本号：缺省 1.0.0（与 go build 不带 -ldflags 时的默认值一致）
# 需要本地调试且不参与更新判断时可 make V=dev
V := 1.0.0

LDFLAGS := -s -w -X fluxor/internal/buildinfo.Version=$(V)

.PHONY: all clean

all:
	@echo "==> 清理旧构建产物"
	@rm -rf frontend/dist backend/dist $(BIN)
	@echo "==> 构建前端 → frontend/dist"
	@cd frontend && $(NPM) run build
	@echo "==> 同步 frontend/dist → backend/dist"
	@rm -rf backend/dist
	@cp -r frontend/dist backend/dist
	@echo "==> 编译后端 → ./$(BIN)  [V=$(V)]"
	@cd backend && CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -o ../$(BIN)
	@echo "==> 完成: ./$(BIN)  (版本 $(V))"

# 仅清理构建产物
clean:
	@rm -rf frontend/dist backend/dist $(BIN)
	@echo "==> 已清理 frontend/dist backend/dist ./$(BIN)"
