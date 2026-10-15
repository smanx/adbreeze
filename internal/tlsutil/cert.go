// Package tlsutil 为局域网 / 远程访问提供 HTTPS 能力。
//
// 动机：WebCodecs（VideoDecoder / EncodedVideoChunk）只在安全上下文暴露，
// 即 HTTPS 或 localhost。用局域网 IP 以 HTTP 访问时浏览器直接不提供这些 API，
// 投屏只能退化到 WebRTC 或 WASM 软解；启用 HTTPS 后这些访问也能走 WebCodecs 硬解。
//
// 局域网 IP 无法申请受信任证书（Let's Encrypt 等需要域名），因此默认自动生成
// 自签名证书；首次访问时浏览器会告警，手动信任一次即可（之后不再提示）。
// 有正式证书的用户可用 -tls-cert / -tls-key 指定。
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	certName = "cert.pem"
	keyName  = "key.pem"

	// 有效期给足：自签名证书一旦过期浏览器会直接拒连，而局域网工具没有轮换证书的
	// 必要；过期后重新生成即可（删除 tls 目录后重启）。
	validity = 10 * 365 * 24 * time.Hour
)

// Ensure 返回可用的证书与私钥路径。
//
// 目录下已有证书时直接复用：保持指纹稳定，避免每次重启都要在浏览器重新信任一次
// （浏览器按证书指纹记忆例外，换了证书就前功尽弃）。缺失时才生成自签名证书。
func Ensure(dir string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(dir, certName)
	keyPath = filepath.Join(dir, keyName)
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建证书目录失败: %w", err)
	}
	if err := generate(certPath, keyPath); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// AdvertisedIPs 返回证书覆盖的本机可用于访问的 IP 列表（供日志与界面展示）。
func AdvertisedIPs() []string {
	ips, _ := sans()
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

// LocalURLs 依据监听地址（如 ":8443"）拼出可直接访问的 HTTPS 地址列表。
func LocalURLs(addr string) []string {
	port := addr
	if _, p, err := net.SplitHostPort(addr); err == nil {
		port = p
	} else {
		port = strings.TrimPrefix(addr, ":")
	}
	if port == "" {
		port = "443"
	}
	urls := []string{"https://localhost:" + port}
	for _, ip := range AdvertisedIPs() {
		if ip == "127.0.0.1" || ip == "::1" {
			continue // 已由 localhost 代表
		}
		urls = append(urls, "https://"+net.JoinHostPort(ip, port))
	}
	return urls
}

// sans 收集证书的 SAN：始终包含 localhost / 回环地址，再加本机所有网卡 IP，
// 这样从任意一台同网设备访问都不会因「证书地址不匹配」被拒。
func sans() (ips []net.IP, dns []string) {
	seen := map[string]bool{}
	add := func(ip net.IP) {
		if ip == nil {
			return
		}
		key := ip.String()
		if seen[key] {
			return
		}
		seen[key] = true
		ips = append(ips, ip)
	}
	add(net.IPv4(127, 0, 0, 1))
	add(net.IPv6loopback)
	dns = []string{"localhost"}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips, dns
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLinkLocalMulticast() {
			continue
		}
		add(ipnet.IP)
	}
	return ips, dns
}

func generate(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("生成私钥失败: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("生成序列号失败: %w", err)
	}
	ips, dns := sans()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "ADBreeze", Organization: []string{"ADBreeze"}},
		// NotBefore 前移一小时：容忍客户端与本机的轻微时钟偏差，避免「证书尚未生效」
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("签发证书失败: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("编码私钥失败: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("写入证书失败: %w", err)
	}
	// 私钥仅属主可读
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("写入私钥失败: %w", err)
	}
	return nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
