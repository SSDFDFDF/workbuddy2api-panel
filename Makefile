# workbuddy_manager — 常用开发/构建/运维入口
#
# 快速开始：
#   make help        # 列出全部目标
#   make run         # 本机跑服务（缺 config.json 时自动从 example 复制）
#   make test        # 完整测试套件（装有 node 时含前端 JS 冒烟）
#   make build       # 构建 wb2api（命令后加 GOOS/GOARCH 可交叉编译）
#   make release     # 五平台二进制 + 打包（对齐 CI 发布口径）
#
# 说明：本仓库不使用构建期 ldflags 注入版本号，版本串来自源码
# cmd/server/main.go 的 appVersion（发布纪律：二进制内含源码版本串）。

SHELL := /bin/bash
.DEFAULT_GOAL := help

# ---- 可覆盖变量 ----------------------------------------------------------
GO        ?= go
GOOS      ?= $(shell $(GO) env GOOS)
GOARCH    ?= $(shell $(GO) env GOARCH)
CGO_ENABLED ?= 0
LDFLAGS   ?= -s -w
CONFIG    ?= config.json
FUZZTIME  ?= 10s
DIST_DIR  ?= dist
PLATFORMS ?= windows/amd64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# 与 Dockerfile / CI 一致的发布构建参数
BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"
EXE         := $(if $(filter windows,$(GOOS)),.exe,)
APP_VERSION := $(shell sed -n 's/.*appVersion = "\([^"]*\)".*/\1/p' cmd/server/main.go | head -1)

# 全部命令入口 → 输出名
CMDS := server:wb2api signin:signin_bin login:login credit:credit

export GOOS GOARCH CGO_ENABLED

# ---- 帮助 ----------------------------------------------------------------
.PHONY: help
help: ## 显示所有目标
	@echo "workbuddy_manager (版本 $(APP_VERSION), host $(shell $(GO) env GOOS)/$(shell $(GO) env GOARCH))"
	@echo ""
	@echo "用法: make <target> [GOOS=... GOARCH=... CONFIG=...]"
	@echo ""
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---- 运行 ----------------------------------------------------------------
.PHONY: run
run: ## 本机启动服务（go run ./cmd/server）
	@test -f $(CONFIG) || { cp config.example.json $(CONFIG); echo "已从 config.example.json 生成 $(CONFIG)（首次启动日志会打印随机 api_key）"; }
	$(GO) run ./cmd/server -config $(CONFIG)

# ---- 前端（React + Vite，产物嵌进 Go 二进制）-----------------------------
FRONTEND_DIR := frontend
WEB_DIR      := internal/panel/web
NPM          ?= npm

.PHONY: frontend
frontend: ## 构建前端（npm run build → internal/panel/web/）
	@test -x "$(shell command -v node 2>/dev/null)" || { echo "缺少 node（跳过前端构建，沿用 $(WEB_DIR)/ 里的已有产物）"; exit 0; }
	cd $(FRONTEND_DIR) && ([ -d node_modules ] || $(NPM) install) && $(NPM) run build
	@echo "→ $(WEB_DIR)/（index.html + app.js + app.css）"

# ---- 构建 ----------------------------------------------------------------
.PHONY: build
build: frontend ## 构建服务端 wb2api（当前平台；先构建前端）
	$(GO) build $(BUILD_FLAGS) -o wb2api$(EXE) ./cmd/server
	@echo "→ wb2api$(EXE) (版本 $(APP_VERSION))"

.PHONY: build-all
build-all: frontend ## 构建全部二进制（server/signin/login/credit；先构建前端）
	@set -e; for c in $(CMDS); do \
	  pkg="$${c%%:*}"; out="$${c##*:}"; \
	  $(GO) build $(BUILD_FLAGS) -o "$$out$(EXE)" "./cmd/$$pkg"; \
	  echo "→ $$out$(EXE)"; \
	done

.PHONY: build-windows
build-windows: ## 交叉编译 Windows amd64（wb2api.exe）
	@$(MAKE) --no-print-directory build GOOS=windows GOARCH=amd64

.PHONY: build-linux
build-linux: ## 交叉编译 Linux amd64（wb2api）
	@$(MAKE) --no-print-directory build GOOS=linux GOARCH=amd64

.PHONY: release
release: ## 五平台二进制 + zip/tar.gz + checksums（输出到 dist/）
	@if printf '%s\n' $(PLATFORMS) | grep -q '^windows/'; then \
	  command -v zip >/dev/null 2>&1 || { echo "缺少 zip（Windows 包需要）"; exit 1; }; \
	fi
	@rm -rf $(DIST_DIR) && mkdir -p $(DIST_DIR)
	@$(MAKE) --no-print-directory frontend
	@set -e; V="$(APP_VERSION)"; V="$${V%-panel}"; \
	for p in $(PLATFORMS); do \
	  os="$${p%%/*}"; arch="$${p##*/}"; ext=""; [ "$$os" = windows ] && ext=".exe"; \
	  echo "==> $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" $(GO) build $(BUILD_FLAGS) -o "$(DIST_DIR)/wb2api$$ext" ./cmd/server; \
	  grep -qa "$(APP_VERSION)" "$(DIST_DIR)/wb2api$$ext" || { echo "二进制缺少版本串 $(APP_VERSION)"; exit 1; }; \
	  cp config.example.json README.md $(DIST_DIR)/; \
	  if [ "$$os" = windows ]; then \
	    (cd $(DIST_DIR) && zip -q "wb2api-panel-v$$V-windows-$$arch.zip" wb2api.exe config.example.json README.md); \
	  else \
	    tar -czf "$(DIST_DIR)/wb2api-panel-v$$V-$$os-$$arch.tar.gz" -C $(DIST_DIR) wb2api config.example.json README.md; \
	  fi; \
	  rm -f "$(DIST_DIR)/wb2api$$ext" $(DIST_DIR)/config.example.json $(DIST_DIR)/README.md; \
	done
	@cd $(DIST_DIR) && find . -maxdepth 1 -type f \( -name '*.zip' -o -name '*.tar.gz' \) \
	  | sed 's|^\./||' | sort | while read -r f; do \
	      if command -v sha256sum >/dev/null 2>&1; then sha256sum "$$f"; else shasum -a 256 "$$f"; fi; \
	    done > checksums.txt
	@echo "→ $(DIST_DIR)/"; ls -lh $(DIST_DIR)

.PHONY: version
version: ## 打印源码版本号
	@echo $(APP_VERSION)

# ---- 官方提示词模板导出（本地参考资料，输出到 docs/，不进版本库）----
# 用法详见 docs/official-templates/README.md；WB_INSTALLER 支持通配符，取版本号最大者。
WB_INSTALLER ?= ../WorkBuddy-win32-x64-user-*.exe
.PHONY: templates-export

templates-export: ## 从官方安装包导出提示词模板（WB_INSTALLER=<exe> 可覆盖）
	@f="$$(ls -1 $(WB_INSTALLER) 2>/dev/null | sort -V | tail -1)"; \
	test -n "$$f" || { echo "找不到安装包：$(WB_INSTALLER)（用 WB_INSTALLER=<exe> 指定）"; exit 1; }; \
	echo "安装包：$$f"; \
	python3 scripts/extract-official-templates.py "$$f"

# ---- 测试与检查 ----------------------------------------------------------
.PHONY: test
test: ## 完整测试套件（node 存在时包含前端 JS 冒烟）
	$(GO) test ./...

.PHONY: test-race
test-race: ## 竞态检测（protocol/server/upstream/forwarding，需 cgo）
	CGO_ENABLED=1 $(GO) test -race ./internal/protocol ./internal/server ./internal/upstream ./internal/forwarding

.PHONY: test-cover
test-cover: ## 覆盖率报告（coverage.out + 函数级汇总）
	$(GO) test -coverprofile=coverage.out ./...
	@$(GO) tool cover -func=coverage.out | tail -1

.PHONY: test-sdk
test-sdk: ## 官方 Python SDK 冒烟（需 uv；仅访问 loopback 假上游）
	@command -v uv >/dev/null 2>&1 || { echo "缺少 uv（https://docs.astral.sh/uv/）"; exit 1; }
	uv run --no-project --with openai==3.26.0 --with anthropic==1.12.1 python -c \
	  'import os,sys,subprocess; os.environ["WB2A_SDK_PYTHON"]=sys.executable; sys.exit(subprocess.call(["go","test","./internal/server","-run","^TestProtocolSDKSmoke$","-v","-count=1"]))'

.PHONY: fuzz
fuzz: ## 协议解析模糊测试（FUZZTIME=10s 可调）
	$(GO) test ./internal/protocol -run '^$$' -fuzz FuzzRequestDecode -fuzztime $(FUZZTIME) -parallel 2

.PHONY: vet
vet: ## go vet 静态检查
	$(GO) vet ./...

.PHONY: fmt
fmt: ## gofmt 格式化源码
	gofmt -l -w cmd internal

.PHONY: fmt-check
fmt-check: ## 检查格式（有未格式化文件则失败）
	@out="$$(gofmt -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "以下文件未格式化（跑 make fmt）："; echo "$$out"; exit 1; fi; \
	echo "gofmt OK"

.PHONY: tidy
tidy: ## 整理依赖
	$(GO) mod tidy

.PHONY: check
check: vet test ## 提交前检查：vet + 测试（对齐 CI 的 test job；格式另跑 fmt-check）

.PHONY: ci
ci: check build-all ## check + 全部二进制构建
	@echo "CI 等价检查通过（版本 $(APP_VERSION)）"

# ---- Docker / 运维 -------------------------------------------------------
.PHONY: docker-build
docker-build: ## 构建镜像
	docker compose build

.PHONY: docker-up
docker-up: ## 启动容器（后台，--build）
	docker compose up -d --build

.PHONY: docker-down
docker-down: ## 停止并移除容器（auths/data 不受影响）
	docker compose down

.PHONY: docker-restart
docker-restart: ## 重启容器
	docker compose restart

.PHONY: docker-logs
docker-logs: ## 跟踪容器日志
	docker compose logs -f

.PHONY: health
health: ## 健康检查（默认 http://localhost:7863/healthz）
	@curl -fsS http://localhost:7863/healthz && echo

# ---- 清理 ----------------------------------------------------------------
.PHONY: clean
clean: ## 删除构建产物（二进制、dist/、coverage.out）
	rm -f wb2api wb2api.exe signin_bin signin_bin.exe login login.exe credit credit.exe coverage.out
	rm -rf $(DIST_DIR) $(WEB_DIR)

.PHONY: clean-dist
clean-dist: ## 只清理 dist/
	rm -rf $(DIST_DIR)
