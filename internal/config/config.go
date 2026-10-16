// Package config 负责运行配置解析，来源有三层，优先级从高到低：
//
//  1. 命令行参数（-addr 等）
//  2. 环境变量（ADBREEZE_ADDR 等）
//  3. 内置默认值
//
// 环境变量的意义在于容器 / 反向代理等不便改命令行的场景：
// docker run -e ADBREEZE_PASSWORD=... 即可，无需改动 ENTRYPOINT。
package config

import (
	"flag"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// envPrefix 是环境变量名前缀，完整名为前缀 + 参数名大写（如 ADBREEZE_ADDR）。
const envPrefix = "ADBREEZE_"

// Config 为 ADBreeze 运行配置。
type Config struct {
	Addr        string // HTTP 监听地址（始终开启，作为保底入口）
	Username    string // Basic Auth 用户名
	Password    string // Basic Auth 密码
	AdbPath     string // adb 可执行文件路径，空则从 PATH 查找
	DataDir     string // 数据目录：存放自动生成的 TLS 证书等（需持久保存，见 Parse）
	ShowVersion bool   // 打印版本号并退出

	// HTTPS：WebCodecs 只在安全上下文（HTTPS / localhost）暴露。
	// 默认开启，与 HTTP 并存而非替代——HTTP 始终可用作保底入口；
	// 证书在浏览器信任之前只能靠 HTTP 进界面，且 HTTPS 配置出错时不至于失联。
	HTTPS     bool   // 是否额外开启 HTTPS
	HTTPSAddr string // HTTPS 监听地址（与 HTTP 分开，避免互相挤占）
	TLSCert   string // 证书路径，留空则自动生成本地 CA + 叶证书
	TLSKey    string // 私钥路径，与 TLSCert 成对使用
	TLSSubdir string // 自动生成证书的存放目录名（位于 DataDir 下）
}

// Parse 解析配置（命令行参数 + 环境变量 + 默认值）。
// 所有 flag 统一在此注册（flag.Parse 只能执行一次）。
func Parse() *Config {
	cfg := &Config{}
	flag.BoolVar(&cfg.ShowVersion, "version", false, "打印版本号并退出")
	flag.StringVar(&cfg.Addr, "addr", ":9080", "HTTP 监听地址（始终开启）")
	flag.StringVar(&cfg.Username, "user", "admin", "Basic Auth 用户名")
	flag.StringVar(&cfg.Password, "password", "admin", "Basic Auth 密码")
	flag.StringVar(&cfg.AdbPath, "adb", "", "adb 可执行文件路径，默认从 PATH 查找")
	flag.StringVar(&cfg.DataDir, "data", "", "数据目录（存放 TLS 证书，需持久保存），默认用户配置目录下的 ADBreeze")
	flag.BoolVar(&cfg.HTTPS, "https", true, "额外开启 HTTPS（默认开启；HTTP 始终可用，-https=false 关闭）")
	flag.StringVar(&cfg.HTTPSAddr, "https-addr", ":9443", "HTTPS 监听地址")
	flag.StringVar(&cfg.TLSCert, "tls-cert", "", "TLS 证书路径，留空则自动生成本地 CA + 叶证书")
	flag.StringVar(&cfg.TLSKey, "tls-key", "", "TLS 私钥路径，与 -tls-cert 成对使用")
	flag.StringVar(&cfg.TLSSubdir, "tls-dir", "tls", "自动生成证书的存放目录名（位于数据目录下）")
	flag.Parse()

	if cfg.ShowVersion {
		return cfg
	}
	applyEnv(cfg)

	// 容忍只写端口号（如 ADBREEZE_ADDR=9080），统一成监听地址形态
	cfg.Addr = normalizeAddr(cfg.Addr)
	cfg.HTTPSAddr = normalizeAddr(cfg.HTTPSAddr)

	defaultDataDir := legacyDataDir()
	if cfg.DataDir == "" {
		// 数据目录必须持久：里面是自动生成的 TLS CA——CA 指纹一旦变化，
		// 用户导入系统信任库的信任即作废、需要重新导入。因此绝不能放临时目录
		// （系统清理临时目录 = 静默换 CA）。默认放用户配置目录，与 settings.json 同根。
		if dir, err := os.UserConfigDir(); err == nil {
			cfg.DataDir = filepath.Join(dir, "ADBreeze")
		} else {
			cfg.DataDir = defaultDataDir
			log.Printf("[config] 无法定位用户配置目录（%v），数据目录退回临时目录；临时目录被系统清理后 CA 会重新生成，已导入的信任将失效", err)
		}
	}
	migrateLegacyDataDir(defaultDataDir, cfg.DataDir)

	// 指定证书时要求成对出现：只有其一无意义，且容易让人以为「已启用证书」
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		log.Fatalf("-tls-cert 与 -tls-key 必须同时指定")
	}
	// 两个监听器同端口会互相挤占：后启动的那个失败并终止进程，连 HTTP 保底也一起没了。
	// 这是纯配置错误，启动即报比运行中莫名失败更好排查。
	if cfg.HTTPS && samePort(cfg.Addr, cfg.HTTPSAddr) {
		log.Fatalf("HTTP 与 HTTPS 不能共用同一端口（%s）；请用 -https-addr 指定不同端口，如 :9443", cfg.Addr)
	}
	return cfg
}

// applyEnv 把环境变量应用到「命令行未显式设置」的配置项上。
// 判断是否显式设置用 flag.Visit：只有命令行里出现过的 flag 才会被访问到，
// 于是天然实现「命令行 > 环境变量 > 默认值」——flag 默认值不会被误判为用户输入。
func applyEnv(cfg *Config) {
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if v := envStr("ADDR"); v != "" && !set["addr"] {
		cfg.Addr = v
	}
	if v := envStr("USER"); v != "" && !set["user"] {
		cfg.Username = v
	}
	if v := envStr("PASSWORD"); v != "" && !set["password"] {
		cfg.Password = v
	}
	if v := envStr("ADB"); v != "" && !set["adb"] {
		cfg.AdbPath = v
	}
	if v := envStr("DATA"); v != "" && !set["data"] {
		cfg.DataDir = v
	}
	if v := envStr("HTTPS"); v != "" && !set["https"] {
		b, err := strconv.ParseBool(v)
		if err != nil {
			log.Printf("[config] 环境变量 ADBREEZE_HTTPS=%q 不是合法布尔值（true/false/1/0），已忽略", v)
		} else {
			cfg.HTTPS = b
		}
	}
	if v := envStr("HTTPS_ADDR"); v != "" && !set["https-addr"] {
		cfg.HTTPSAddr = v
	}
	if v := envStr("TLS_CERT"); v != "" && !set["tls-cert"] {
		cfg.TLSCert = v
	}
	if v := envStr("TLS_KEY"); v != "" && !set["tls-key"] {
		cfg.TLSKey = v
	}
	if v := envStr("TLS_DIR"); v != "" && !set["tls-dir"] {
		cfg.TLSSubdir = v
	}
}

// envStr 读取环境变量并去掉首尾空白。
func envStr(name string) string {
	return strings.TrimSpace(os.Getenv(envPrefix + name))
}

// normalizeAddr 容忍纯端口号写法（"9080"），统一补成监听地址（":9080"）。
// 含冒号的写法（":9080" / "0.0.0.0:9080" / "[::1]:9080"）原样保留。
func normalizeAddr(a string) string {
	if a == "" || strings.Contains(a, ":") {
		return a
	}
	return ":" + a
}

// legacyDataDir 旧版默认数据目录：系统临时目录下的 ADBreeze。
// 旧版把 TLS 证书放在这里，系统清理临时目录会导致 CA 静默重建、信任作废。
func legacyDataDir() string {
	return filepath.Join(os.TempDir(), "ADBreeze")
}

// migrateLegacyDataDir 把旧临时目录里的证书一次性搬到新数据目录。
// 只在「旧目录有 CA + 新目录没有 CA」时执行：幂等，且绝不覆盖新目录已有数据。
// 迁移的是证书文件本身，指纹不变，用户已导入的信任继续有效。
func migrateLegacyDataDir(oldDir, newDir string) {
	if oldDir == newDir {
		return
	}
	oldTLS := filepath.Join(oldDir, "tls")
	newTLS := filepath.Join(newDir, "tls")
	if _, err := os.Stat(filepath.Join(oldTLS, "ca.pem")); err != nil {
		return // 旧目录没有 CA：无事可做
	}
	if _, err := os.Stat(filepath.Join(newTLS, "ca.pem")); err == nil {
		return // 新目录已有 CA：以新目录为准，不覆盖
	}
	if err := os.MkdirAll(newTLS, 0o700); err != nil {
		log.Printf("[config] 旧证书迁移失败（不影响使用，下次启动重试）: %v", err)
		return
	}
	for _, name := range []string{"ca.pem", "ca.key", "cert.pem", "key.pem"} {
		b, err := os.ReadFile(filepath.Join(oldTLS, name))
		if err != nil {
			log.Printf("[config] 旧证书迁移中断（不影响使用，下次启动重试）: %s: %v", name, err)
			return
		}
		if err := os.WriteFile(filepath.Join(newTLS, name), b, 0o600); err != nil {
			log.Printf("[config] 旧证书迁移中断（不影响使用，下次启动重试）: %s: %v", name, err)
			return
		}
	}
	_ = os.RemoveAll(oldTLS) // 搬完清掉旧目录，避免残留半套证书混淆排查
	log.Printf("[config] 已把 TLS 证书从临时目录迁移到持久目录（CA 指纹不变，无需重新导入信任）")
}

// samePort 比较两个监听地址的端口是否相同（地址格式可为 ":9080" 或 "0.0.0.0:9080"）。
func samePort(a, b string) bool {
	return portOf(a) == portOf(b)
}

func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return strings.TrimPrefix(addr, ":")
}
