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
#
# 数据目录 /config 存放自动生成的 TLS CA 与叶证书：CA 指纹必须跨容器重启保持稳定
# （导入信任库只需一次），因此务必用 -v 挂载持久化，否则容器重建后 CA 重新生成、信任作废。
FROM alpine:3.20
RUN apk add --no-cache android-tools ca-certificates && adduser -D adbreeze \
    && mkdir -p /config && chown adbreeze:adbreeze /config
COPY --from=go-builder /out/adbreeze /usr/local/bin/adbreeze
USER adbreeze
VOLUME ["/config"]
ENV ADBREEZE_DATA=/config
EXPOSE 9080 9443
ENTRYPOINT ["adbreeze"]
