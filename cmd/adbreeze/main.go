package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"adbreeze"
	"adbreeze/internal/adb"
	"adbreeze/internal/api"
	"adbreeze/internal/auth"
	"adbreeze/internal/config"
	"adbreeze/internal/scrcpy"
	"adbreeze/internal/settings"
	"adbreeze/internal/tlsutil"
)

// version 由构建时 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	cfg := config.Parse()
	if cfg.ShowVersion {
		fmt.Println("ADBreeze", version)
		return
	}
	log.Printf("ADBreeze %s 启动，监听 %s", version, cfg.Addr)
	log.Printf("登录账号: %s / 密码: %s", cfg.Username, cfg.Password)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 用户偏好（adb 路径等）：网页里可改，重启后仍生效。
	// 存档里的值优先于命令行 -adb：命令行是启动基线，界面改动是用户的最新意图。
	st, err := settings.Open()
	if err != nil {
		log.Printf("[settings] 读取用户设置失败，改用内存态（改动不会持久化）: %v", err)
		st = settings.NewInMemory()
	}
	adbPath := cfg.AdbPath
	if st.Get().AdbPath != "" {
		adbPath = st.Get().AdbPath
		log.Printf("[settings] 使用已保存的 adb 路径: %s", adbPath)
	}

	// ADB 层：server 不可达不阻断启动，跟踪循环会持续重试
	mgr := adb.NewManager(adbPath, "")
	if err := mgr.Start(ctx); err != nil {
		log.Printf("[adb] adb server 暂不可用，将自动重试: %v", err)
	}

	// 投屏层（生命周期与应用一致，不随单个请求结束）
	mirror := scrcpy.NewManager(ctx, mgr, adbreeze.ScrcpyServer, adbreeze.ScrcpyServerVersion)

	webFS, err := fs.Sub(adbreeze.WebFS, "web/dist")
	if err != nil {
		log.Fatalf("内嵌前端资源缺失: %v", err)
	}

	handler := api.New(auth.New(cfg.Username, cfg.Password), version, mgr, mirror, webFS, st)

	// HTTP 始终开启：它是保底入口——自签名证书在浏览器信任之前只能靠它进界面，
	// 且 HTTPS 配置出错时不会导致完全失联。
	httpSrv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()

	// HTTPS 可选叠加（不同端口）：WebCodecs 只在安全上下文暴露，用局域网 IP
	// 以 HTTP 访问时浏览器不提供 VideoDecoder，投屏会退化到 WebRTC / 软解。
	var httpsSrv *http.Server
	if cfg.HTTPS {
		certPath, keyPath := cfg.TLSCert, cfg.TLSKey
		var tlsDir string
		// 用户显式追加的证书覆盖名字（-tls-hosts / -tls-ips，环境变量
		// ADBREEZE_TLS_HOSTS / ADBREEZE_TLS_IPS）：自动探测只覆盖本机网卡 IP
		// 与主机名，Docker 桥接 / NAT 后的外部地址、自定义域名靠这里补。
		// 非法条目不致命：记日志后忽略，其余合法配置照常生效。
		extra, extraErrs := tlsutil.ParseExtraNames(cfg.TLSHosts, cfg.TLSIPs)
		for _, e := range extraErrs {
			log.Printf("[tls] 忽略非法的证书覆盖名字配置: %v", e)
		}
		if certPath == "" {
			// 未指定证书：用本地 CA 签发叶证书（局域网 IP 申请不到受信任证书）。
			// CA 长期不变，叶证书随当前网卡 IP / 主机名自动重签，故只需信任 CA 一次。
			tlsDir = filepath.Join(cfg.DataDir, cfg.TLSSubdir)
			certPath, keyPath, err = tlsutil.Ensure(tlsDir, extra)
			if err != nil {
				log.Fatalf("准备证书失败: %v", err)
			}
		}
		httpsSrv = &http.Server{Addr: cfg.HTTPSAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		cp, kp, addr := certPath, keyPath, cfg.HTTPSAddr
		go func() {
			if err := httpsSrv.ListenAndServeTLS(cp, kp); err != nil && err != http.ErrServerClosed {
				// 不 Fatalf：HTTP 仍在服务，不该因为 HTTPS 起不来就把整个进程带走
				log.Printf("[tls] HTTPS 服务失败（HTTP 仍可用）: %v", err)
			}
		}()
		log.Printf("HTTPS 就绪（安全上下文，WebCodecs 可用）: https://localhost%s", addr)
		for _, u := range tlsutil.LocalURLs(addr, extra) {
			log.Printf("       %s", u)
		}
		if tlsDir != "" {
			dns, ips := tlsutil.CoveredNames(extra)
			log.Printf("       证书覆盖主机名: %s", strings.Join(dns, ", "))
			log.Printf("       证书覆盖 IP: %s", strings.Join(ips, ", "))
			log.Printf("       首次使用请把 CA 导入系统信任库（只需一次，之后 IP/主机名变化都免重导）:")
			log.Printf("         %s", tlsutil.CACertPath(tlsDir))
		}
	}

	log.Printf("就绪: http://localhost%s", cfg.Addr)
	if !cfg.HTTPS {
		log.Printf("      （WebCodecs 需安全上下文；局域网 IP 访问请加 -https 开启 HTTPS）")
	}

	<-ctx.Done()
	log.Println("正在退出...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if httpsSrv != nil {
		_ = httpsSrv.Shutdown(shutdownCtx)
	}
}
