// Package tlsutil 为局域网 / 远程访问提供 HTTPS 能力。
//
// 动机：WebCodecs（VideoDecoder / EncodedVideoChunk）只在安全上下文暴露，
// 即 HTTPS 或 localhost。用局域网 IP 以 HTTP 访问时浏览器直接不提供这些 API，
// 投屏只能退化到 WebRTC 或 WASM 软解；开启 HTTPS 后这些访问也能走 WebCodecs 硬解。
//
// 局域网 IP 无法申请受信任证书（Let's Encrypt 等需要域名），因此默认自动生成自签名证书；
// 首次访问时浏览器会告警，手动信任一次即可（之后不再提示）。
// 有正式证书的用户可用 -tls-cert / -tls-key 指定。
//
// # 为什么是「本地 CA + 叶证书」而不是单个自签证书
//
// 运行环境的主机 IP 与主机名都可能变化，而浏览器判定 origin 是否可信时要求证书 SAN
// 覆盖当前访问用的那个名字。若直接用单个自签证书，每次名字集变化都要重新签发，
// 指纹随之改变，用户之前导入的信任立即失效，必须重新导入——环境一变就要再做一次。
//
// 因此改为两层结构：CA 证书长期不变（指纹稳定，作为信任锚只导入一次），
// 叶证书每次启动按当前网卡 IP + 主机名重新计算 SAN，仅在名字集变化时重签，
// 重签不影响 CA 指纹，用户无需再信任。这也是 mkcert 的做法。
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
	caCertName = "ca.pem"
	caKeyName  = "ca.key"
	certName   = "cert.pem"
	keyName    = "key.pem"

	// 有效期给足：证书过期浏览器会直接拒连，而局域网工具没有轮换证书的必要；
	// 过期后删除 tls 目录重启即可重新生成。
	validity = 10 * 365 * 24 * time.Hour
	// 签名时钟容差：NotBefore 前移一小时，容忍客户端与本机的轻微时钟偏差
	clockSkew = time.Hour
)

// certNames 是一份证书需要覆盖的名字集合。
type certNames struct {
	dns []string
	ips []net.IP
}

// key 生成与顺序无关的指纹串，用于判断「名字集合是否变化」。
// 网卡枚举顺序在不同次启动可能不同，因此必须先排序再比较。
func (n certNames) key() string {
	d := append([]string(nil), n.dns...)
	sort.Strings(d)
	ips := make([]string, 0, len(n.ips))
	for _, ip := range n.ips {
		ips = append(ips, ip.String())
	}
	sort.Strings(ips)
	return strings.Join(d, ",") + "|" + strings.Join(ips, ",")
}

// Ensure 返回可用的证书与私钥路径（叶证书优先，CA 作为信任锚）。
//
// 已有叶证书且其名字集与当前环境一致时直接复用：保持指纹稳定，
// 避免每次重启都要在浏览器重新信任一次（浏览器按证书指纹记忆例外）。
func Ensure(dir string) (certPath, keyPath string, err error) {
	return ensureWith(dir, sans())
}

// ensureWith 是 Ensure 的可测版本：允许注入名字集合，便于测试「名字变化触发重签」。
func ensureWith(dir string, want certNames) (certPath, keyPath string, err error) {
	certPath = filepath.Join(dir, certName)
	keyPath = filepath.Join(dir, keyName)
	caCertPath := filepath.Join(dir, caCertName)
	caKeyPath := filepath.Join(dir, caKeyName)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建证书目录失败: %w", err)
	}

	caCert, caKey, err := loadOrCreateCA(caCertPath, caKeyPath)
	if err != nil {
		return "", "", err
	}

	// 叶证书仍满足「由当前 CA 签发 + 名字集一致 + 未过期」时复用
	if leafUsable(certPath, keyPath, caCert, want) {
		return certPath, keyPath, nil
	}
	if err := signLeaf(certPath, keyPath, caCert, caKey, want); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// CACertPath 返回 CA 证书路径（供界面提示用户导入哪一个文件）。
func CACertPath(dir string) string { return filepath.Join(dir, caCertName) }

// ---------- CA ----------

func loadOrCreateCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if c, k, err := loadCA(certPath, keyPath); err == nil {
		return c, k, nil
	}
	c, k, err := createCA(certPath, keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("生成 CA 失败: %w", err)
	}
	return c, k, nil
}

func loadCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return nil, nil, err
	}
	if !cert.IsCA {
		return nil, nil, fmt.Errorf("CA 证书缺少 IsCA 标记")
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func createCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("生成 CA 私钥失败: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ADBreeze Local CA", Organization: []string{"ADBreeze"}},
		NotBefore:             time.Now().Add(-clockSkew),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true, // 只签发终端叶证书，不再往下发中间 CA
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("签发 CA 失败: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 CA 失败: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("编码 CA 私钥失败: %w", err)
	}
	if err := writeFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, err
	}
	if err := writeFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// ---------- 叶证书 ----------

// leafUsable 判断现有叶证书能否继续用：公私钥配对、由当前 CA 签发、
// 名字集与当前环境一致、未过期。任一条不满足都重签。
func leafUsable(certPath, keyPath string, caCert *x509.Certificate, want certNames) bool {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return false
	}
	cert, err := parseCert(certPEM) // 取链中第一张（叶）
	if err != nil {
		return false
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return false
	}
	if !samePublicKey(&key.PublicKey, cert.PublicKey) {
		return false
	}
	// 由当前 CA 签发：换过 CA（如旧版单证书升级到 CA 结构）后旧叶必须重签，否则链不上
	if cert.CheckSignatureFrom(caCert) != nil {
		return false
	}
	// 名字集一致：网卡 IP / 主机名变化 → 重签（CA 不变，用户无需再信任）
	if leafNames(cert).key() != want.key() {
		return false
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter.Add(-24*time.Hour)) {
		return false
	}
	return true
}

func leafNames(cert *x509.Certificate) certNames {
	ips := make([]net.IP, 0, len(cert.IPAddresses))
	ips = append(ips, cert.IPAddresses...)
	return certNames{dns: append([]string(nil), cert.DNSNames...), ips: ips}
}

func signLeaf(certPath, keyPath string, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, want certNames) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("生成叶证书私钥失败: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ADBreeze", Organization: []string{"ADBreeze"}},
		NotBefore:             time.Now().Add(-clockSkew),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string(nil), want.dns...),
		IPAddresses:           append([]net.IP(nil), want.ips...),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("签发叶证书失败: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("编码叶证书私钥失败: %w", err)
	}
	// 服务端链：叶在前、CA 在后。用户已把 CA 导入信任库时叶单独也够，
	// 附上 CA 让未导入的客户端也能看到完整链，排查更直观。
	chain := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})...,
	)
	if err := writeFile(certPath, chain, 0o644); err != nil {
		return err
	}
	return writeFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)
}

// ---------- 名字集合 ----------

// sans 收集叶证书的 SAN：始终包含 localhost / 回环地址，再加本机主机名与所有网卡 IP。
//
// 主机名比 IP 稳定得多：同一台机器换网络时 IP 会变，主机名通常不变，
// 因此把主机名写进 SAN 能让「用主机名访问」这条路在换网后依然免信任。
func sans() certNames {
	seen := map[string]bool{}
	var ips []net.IP
	add := func(ip net.IP) {
		if ip == nil {
			return
		}
		k := ip.String()
		if seen[k] {
			return
		}
		seen[k] = true
		ips = append(ips, ip)
	}
	add(net.IPv4(127, 0, 0, 1))
	add(net.IPv6loopback)

	dns := []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" && !strings.EqualFold(h, "localhost") {
		dns = append(dns, h)
	}

	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLinkLocalMulticast() {
				continue
			}
			add(ipnet.IP)
		}
	}
	return certNames{dns: dns, ips: ips}
}

// CoveredNames 返回当前证书覆盖的主机名与 IP（供启动日志与界面展示）。
func CoveredNames() (dns []string, ips []string) {
	n := sans()
	dns = append(dns, n.dns...)
	for _, ip := range n.ips {
		ips = append(ips, ip.String())
	}
	sort.Strings(ips)
	return dns, ips
}

// AdvertisedIPs 返回可用于访问的本机 IP 列表（去掉由 localhost 代表的回环地址）。
func AdvertisedIPs() []string {
	_, ips := CoveredNames()
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == "127.0.0.1" || ip == "::1" {
			continue
		}
		out = append(out, ip)
	}
	return out
}

// LocalURLs 依据监听地址（如 ":8443"）拼出可直接访问的 HTTPS 地址列表，
// 主机名与 IP 都列出——主机名那条在换网后仍免信任，是更稳的入口。
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
	dns, _ := CoveredNames()
	urls := make([]string, 0, len(dns)+len(AdvertisedIPs()))
	for _, h := range dns {
		urls = append(urls, "https://"+net.JoinHostPort(h, port))
	}
	for _, ip := range AdvertisedIPs() {
		urls = append(urls, "https://"+net.JoinHostPort(ip, port))
	}
	return urls
}

// ---------- 工具 ----------

func parseCert(b []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("证书 PEM 解析失败")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKey(b []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("私钥 PEM 解析失败")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("私钥类型不是 ECDSA")
	}
	return ec, nil
}

func samePublicKey(a, b any) bool {
	ka, ok1 := a.(*ecdsa.PublicKey)
	kb, ok2 := b.(*ecdsa.PublicKey)
	if !ok1 || !ok2 {
		return false
	}
	return ka.X.Cmp(kb.X) == 0 && ka.Y.Cmp(kb.Y) == 0
}

func randomSerial() (*big.Int, error) {
	s, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("生成序列号失败: %w", err)
	}
	return s, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", filepath.Base(path), err)
	}
	return nil
}
