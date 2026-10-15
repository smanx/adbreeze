// Package config 负责命令行配置解析。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

// Config 为 ADBreeze 运行配置。
type Config struct {
	Addr        string // HTTP 监听地址（始终开启，作为保底入口）
	Username    string // Basic Auth 用户名
	Password    string // Basic Auth 密码
	AdbPath     string // adb 可执行文件路径，空则从 PATH 查找
	DataDir     string // 数据目录（后续用于释放内置 adb 二进制等资源）
	ShowVersion bool   // 打印版本号并退出

	// HTTPS：WebCodecs 只在安全上下文（HTTPS / localhost）暴露，
	// 用局域网 IP 以 HTTP 访问时浏览器不提供 VideoDecoder，投屏会退化。
	// 默认关闭（HTTP 即可用）；开启后与 HTTP 并存，不是替代关系——
	// 自签名证书在浏览器信任之前只能靠 HTTP 进界面，且 HTTPS 配置出错时不至于失联。
	HTTPS     bool   // 是否额外开启 HTTPS
	HTTPSAddr string // HTTPS 监听地址（与 HTTP 分开，避免互相挤占）
	TLSCert   string // 证书路径，留空则自动生成自签名证书
	TLSKey    string // 私钥路径，与 TLSCert 成对使用
	TLSSubdir string // 自动生成证书的存放目录名（位于 DataDir 下）
}

// Parse 解析命令行参数。所有 flag 统一在此注册（flag.Parse 只能执行一次）。
func Parse() *Config {
	cfg := &Config{}
	flag.BoolVar(&cfg.ShowVersion, "version", false, "打印版本号并退出")
	flag.StringVar(&cfg.Addr, "addr", ":8000", "HTTP 监听地址（始终开启）")
	flag.StringVar(&cfg.Username, "user", "admin", "Basic Auth 用户名")
	flag.StringVar(&cfg.Password, "password", "", "Basic Auth 密码，默认随机生成")
	flag.StringVar(&cfg.AdbPath, "adb", "", "adb 可执行文件路径，默认从 PATH 查找")
	flag.StringVar(&cfg.DataDir, "data", "", "数据目录，默认使用系统临时目录下的 ADBreeze")
	flag.BoolVar(&cfg.HTTPS, "https", false, "额外开启 HTTPS（WebCodecs 需安全上下文；默认关闭，HTTP 始终可用）")
	flag.StringVar(&cfg.HTTPSAddr, "https-addr", ":8443", "HTTPS 监听地址（仅在 -https 时生效）")
	flag.StringVar(&cfg.TLSCert, "tls-cert", "", "TLS 证书路径，留空则自动生成自签名证书")
	flag.StringVar(&cfg.TLSKey, "tls-key", "", "TLS 私钥路径，与 -tls-cert 成对使用")
	flag.StringVar(&cfg.TLSSubdir, "tls-dir", "tls", "自签名证书存放目录名（位于数据目录下）")
	flag.Parse()

	if cfg.ShowVersion {
		return cfg
	}
	if cfg.Password == "" {
		cfg.Password = randomPassword()
	}
	if cfg.DataDir == "" {
		cfg.DataDir = fmt.Sprintf("%s/ADBreeze", os.TempDir())
	}
	// 指定证书时要求成对出现：只有其一无意义，且容易让人以为「已启用证书」
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		log.Fatalf("-tls-cert 与 -tls-key 必须同时指定")
	}
	// 两个监听器同端口会互相挤占：后启动的那个失败并终止进程，连 HTTP 保底也一起没了。
	// 这是纯配置错误，启动即报比运行中莫名失败更好排查。
	if cfg.HTTPS && samePort(cfg.Addr, cfg.HTTPSAddr) {
		log.Fatalf("HTTP 与 HTTPS 不能共用同一端口（%s）；请用 -https-addr 指定不同端口，如 :8443", cfg.Addr)
	}
	return cfg
}

// samePort 比较两个监听地址的端口是否相同（地址格式可为 ":8000" 或 "0.0.0.0:8000"）。
func samePort(a, b string) bool {
	return portOf(a) == portOf(b)
}

func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return strings.TrimPrefix(addr, ":")
}

func randomPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("生成随机密码失败: %v", err)
	}
	return hex.EncodeToString(b)
}
