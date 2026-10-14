# ---- 构建阶段 ----
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/adbreeze ./cmd/adbreeze

# ---- 运行阶段 ----
# 前端与 scrcpy-server 已 embed 进二进制；容器内自带 adb
# （服务启动时自动 start-server），可通过 adb connect 管理网络设备。
# USB 直连需 --privileged 并挂载 /dev/bus/usb。
FROM alpine:3.20
RUN apk add --no-cache android-tools ca-certificates && adduser -D adbreeze
COPY --from=builder /out/adbreeze /usr/local/bin/adbreeze
USER adbreeze
ENTRYPOINT ["adbreeze"]
