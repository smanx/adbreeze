// Package config 负责命令行配置解析。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
)

// Config 为 ADBreeze 运行配置。
type Config struct {
	Addr        string // HTTP 监听地址
	Username    string // Basic Auth 用户名
	Password    string // Basic Auth 密码
	AdbPath     string // adb 可执行文件路径，空则从 PATH 查找
	DataDir     string // 数据目录（后续用于释放内置 adb 二进制等资源）
	ShowVersion bool   // 打印版本号并退出
}

// Parse 解析命令行参数。所有 flag 统一在此注册（flag.Parse 只能执行一次）。
func Parse() *Config {
	cfg := &Config{}
	flag.BoolVar(&cfg.ShowVersion, "version", false, "打印版本号并退出")
	flag.StringVar(&cfg.Addr, "addr", ":8000", "HTTP 监听地址")
	flag.StringVar(&cfg.Username, "user", "admin", "Basic Auth 用户名")
	flag.StringVar(&cfg.Password, "password", "", "Basic Auth 密码，默认随机生成")
	flag.StringVar(&cfg.AdbPath, "adb", "", "adb 可执行文件路径，默认从 PATH 查找")
	flag.StringVar(&cfg.DataDir, "data", "", "数据目录，默认使用系统临时目录下的 ADBreeze")
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
	return cfg
}

func randomPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("生成随机密码失败: %v", err)
	}
	return hex.EncodeToString(b)
}
