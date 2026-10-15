# ---- 前端构建阶段 ----
# 产出 web/dist，供 Go 阶段 go:embed 嵌入（assets.go 依赖此目录，缺失则编译失败）
FROM node:22-alpine AS web-builder
RUN corepack enable
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

# ---- Go 构建阶段 ----
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web-builder /web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/adbreeze ./cmd/adbreeze

# ---- 运行阶段 ----
# 前端与 scrcpy-server 已 embed 进二进制；容器内自带 adb
# （服务启动时自动 start-server），可通过 adb connect 管理网络设备。
# USB 直连需 --privileged 并挂载 /dev/bus/usb。
FROM alpine:3.20
RUN apk add --no-cache android-tools ca-certificates && adduser -D adbreeze
COPY --from=go-builder /out/adbreeze /usr/local/bin/adbreeze
USER adbreeze
ENTRYPOINT ["adbreeze"]
