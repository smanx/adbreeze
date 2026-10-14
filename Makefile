BINARY  := adbreeze
VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

TARGETS := windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build build-all docker clean

## build: 编译当前平台
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)$(shell go env GOEXE) ./cmd/adbreeze

## build-all: 交叉编译全部目标平台（前端已 embed，产物为单文件）
build-all:
	@mkdir -p bin
	@set -e; for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; \
		[ "$$os" = "windows" ] && ext=".exe"; \
		echo "==> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" \
			-o bin/$(BINARY)-$$os-$$arch$$ext ./cmd/adbreeze; \
	done

## docker: 构建 Docker 镜像（alpine + android-tools，容器内自带 adb 可连网络设备）
docker:
	docker build -t $(BINARY):$(VERSION) .

clean:
	rm -rf bin
