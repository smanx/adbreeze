package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"adbreeze"
	"adbreeze/internal/adb"
	"adbreeze/internal/api"
	"adbreeze/internal/auth"
	"adbreeze/internal/config"
	"adbreeze/internal/scrcpy"
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

	// ADB 层：server 不可达不阻断启动，跟踪循环会持续重试
	mgr := adb.NewManager(cfg.AdbPath, "")
	if err := mgr.Start(ctx); err != nil {
		log.Printf("[adb] adb server 暂不可用，将自动重试: %v", err)
	}

	// 投屏层（生命周期与应用一致，不随单个请求结束）
	mirror := scrcpy.NewManager(ctx, mgr, adbreeze.ScrcpyServer, adbreeze.ScrcpyServerVersion)

	webFS, err := fs.Sub(adbreeze.WebFS, "web/dist")
	if err != nil {
		log.Fatalf("内嵌前端资源缺失: %v", err)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(auth.New(cfg.Username, cfg.Password), version, mgr, mirror, webFS),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务失败: %v", err)
		}
	}()
	log.Printf("就绪: http://localhost%s", cfg.Addr)

	<-ctx.Done()
	log.Println("正在退出...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
