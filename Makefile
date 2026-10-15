BINARY  := adbreeze
VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

TARGETS := windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

# go:embed 的前端产物（assets.go 引用 web/dist）；CI/本地都走这一条契约。
# 任一前端源文件比产物新即触发重建，避免打包出陈旧前端。
WEB_STAMP  := web/dist/index.html
WEB_INPUTS := $(wildcard web/index.html web/vite.config.ts web/package.json web/pnpm-lock.yaml) \
              $(wildcard web/src/*.tsx web/src/*.ts web/src/*.css) \
              $(wildcard web/src/*/*.tsx web/src/*/*.ts) \
              $(wildcard web/public/*)

.PHONY: build build-all web docker clean

## web: 构建前端产物 web/dist
web: $(WEB_STAMP)

$(WEB_STAMP): $(WEB_INPUTS)
	cd web && pnpm install --frozen-lockfile && pnpm run build

## build: 编译当前平台（前端已 embed，需先有 web/dist）
build: $(WEB_STAMP)
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)$(shell go env GOEXE) ./cmd/adbreeze

## build-all: 交叉编译全部目标平台（前端已 embed，产物为单文件）
build-all: $(WEB_STAMP)
	@mkdir -p bin
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		echo "==> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" \
			-o bin/$(BINARY)-$$os-$$arch$$ext ./cmd/adbreeze; \
	done

## docker: 构建镜像（多阶段自含前端构建，不依赖本地 web/dist）
docker:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

clean:
	rm -rf bin
