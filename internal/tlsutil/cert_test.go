package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成测试私钥失败: %v", err)
	}
	return k
}

func pkixName(cn string) pkix.Name { return pkix.Name{CommonName: cn, Organization: []string{"ADBreeze"}} }

// names 构造一个测试用的名字集合。
func names(dns []string, ips []string) certNames {
	out := certNames{dns: dns}
	for _, s := range ips {
		out.ips = append(out.ips, net.ParseIP(s))
	}
	return out
}

func readLeaf(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取叶证书失败: %v", err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("叶证书 PEM 解析失败")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("解析叶证书失败: %v", err)
	}
	return c
}

func readCACert(t *testing.T, dir string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, caCertName))
	if err != nil {
		t.Fatalf("读取 CA 失败: %v", err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("CA PEM 解析失败")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("解析 CA 失败: %v", err)
	}
	return c
}

// 首次调用应生成 CA 与叶证书，且叶证书由该 CA 签发、SAN 覆盖期望名字。
func TestEnsureCreatesCAAndLeaf(t *testing.T) {
	dir := t.TempDir()
	want := names([]string{"localhost", "myhost"}, []string{"127.0.0.1", "10.1.2.3"})

	certPath, keyPath, err := ensureWith(dir, want)
	if err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("叶证书未生成: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("叶私钥未生成: %v", err)
	}

	ca := readCACert(t, dir)
	if !ca.IsCA {
		t.Error("CA 证书缺少 IsCA 标记")
	}

	leaf := readLeaf(t, certPath)
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Errorf("叶证书并非由该 CA 签发: %v", err)
	}
	if got := leafNames(leaf).key(); got != want.key() {
		t.Errorf("SAN 不匹配:\n got=%s\nwant=%s", got, want.key())
	}
}

// 名字集合不变时复用同一张叶证书（指纹稳定，用户无需重新信任）。
func TestEnsureReusesLeafWhenNamesUnchanged(t *testing.T) {
	dir := t.TempDir()
	want := names([]string{"localhost", "myhost"}, []string{"127.0.0.1", "10.1.2.3"})

	if _, _, err := ensureWith(dir, want); err != nil {
		t.Fatalf("首次 Ensure 失败: %v", err)
	}
	first := readLeaf(t, filepath.Join(dir, certName))

	if _, _, err := ensureWith(dir, want); err != nil {
		t.Fatalf("二次 Ensure 失败: %v", err)
	}
	second := readLeaf(t, filepath.Join(dir, certName))

	if first.SerialNumber.Cmp(second.SerialNumber) != 0 {
		t.Error("名字未变却重签了叶证书（指纹不稳定，用户会白信任一次）")
	}
}

// 名字集合变化（换网络 / 换主机名）时重签叶证书，但 CA 保持不变。
func TestEnsureResignsLeafWhenNamesChange(t *testing.T) {
	dir := t.TempDir()
	base := names([]string{"localhost", "myhost"}, []string{"127.0.0.1", "10.1.2.3"})
	changed := names([]string{"localhost", "myhost"}, []string{"127.0.0.1", "10.9.9.9"})

	if _, _, err := ensureWith(dir, base); err != nil {
		t.Fatalf("首次 Ensure 失败: %v", err)
	}
	caBefore := readCACert(t, dir)
	leafBefore := readLeaf(t, filepath.Join(dir, certName))

	if _, _, err := ensureWith(dir, changed); err != nil {
		t.Fatalf("换名字后 Ensure 失败: %v", err)
	}
	caAfter := readCACert(t, dir)
	leafAfter := readLeaf(t, filepath.Join(dir, certName))

	if leafBefore.SerialNumber.Cmp(leafAfter.SerialNumber) == 0 {
		t.Error("名字变化后未重签叶证书")
	}
	// CA 必须稳定：否则用户导入的信任作废，又要重导一次
	if !caBefore.Equal(caAfter) {
		t.Error("CA 被重新生成了（用户信任会作废）")
	}
	// 新叶仍应挂在同一个 CA 下
	if err := leafAfter.CheckSignatureFrom(caAfter); err != nil {
		t.Errorf("重签后的叶证书未由 CA 签发: %v", err)
	}
	if got := leafNames(leafAfter).key(); got != changed.key() {
		t.Errorf("重签后 SAN 不对:\n got=%s\nwant=%s", got, changed.key())
	}
}

// 名字集合的比较必须与枚举顺序无关（网卡顺序在不同次启动会变）。
func TestNamesKeyOrderInsensitive(t *testing.T) {
	a := names([]string{"localhost", "host"}, []string{"10.0.0.2", "10.0.0.1"})
	b := names([]string{"host", "localhost"}, []string{"10.0.0.1", "10.0.0.2"})
	if a.key() != b.key() {
		t.Errorf("名字集合比较受顺序影响: %s != %s", a.key(), b.key())
	}
}

// 旧的单证书（非 CA 结构）升级到 CA 结构时，叶证书应被重签并挂到新 CA 下。
func TestUpgradeFromSelfSignedToCA(t *testing.T) {
	dir := t.TempDir()
	// 手工造一份「旧版」单证书：自签（非 CA），再让 Ensure 接管
	key := mustKey(t)
	serial, _ := randomSerial()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkixName("ADBreeze"),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("造旧证书失败: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(dir, certName), certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, keyName), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	want := names([]string{"localhost"}, []string{"127.0.0.1"})
	if _, _, err := ensureWith(dir, want); err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	ca := readCACert(t, dir)
	leaf := readLeaf(t, filepath.Join(dir, certName))
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Errorf("升级后叶证书未挂到新 CA 下: %v", err)
	}
}

// ---------- ExtraNames ----------

// ParseExtraNames：主机名里的 IP 形式条目应归位到 IPs；
// 非法 IP 记错误但不丢合法条目（不致命，记日志后继续用）。
func TestParseExtraNames(t *testing.T) {
	extra, errs := ParseExtraNames("adb.example.com, 10.1.2.3, [::1]", "203.0.113.10, not-an-ip")
	if len(errs) != 1 {
		t.Fatalf("应有 1 个非法 IP 错误，得到 %d 个: %v", len(errs), errs)
	}
	if len(extra.DNS) != 1 || extra.DNS[0] != "adb.example.com" {
		t.Errorf("DNS 不匹配: got=%v want=[adb.example.com]", extra.DNS)
	}
	wantIPs := map[string]bool{"10.1.2.3": true, "::1": true, "203.0.113.10": true}
	if len(extra.IPs) != len(wantIPs) {
		t.Fatalf("IP 数量不对: got=%d want=%d", len(extra.IPs), len(wantIPs))
	}
	for _, ip := range extra.IPs {
		if !wantIPs[ip.String()] {
			t.Errorf("多出非预期 IP: %s", ip)
		}
	}
}

// ParseExtraNames 容忍空白与空条目（"a, ,b," 是手写/粘贴时的常态）。
func TestParseExtraNamesToleratesBlankEntries(t *testing.T) {
	extra, errs := ParseExtraNames(" adb.example.com ,, ", " , ")
	if len(errs) != 0 {
		t.Fatalf("空白条目不应报错: %v", errs)
	}
	if len(extra.DNS) != 1 || extra.DNS[0] != "adb.example.com" {
		t.Errorf("DNS 解析不对: %v", extra.DNS)
	}
	if len(extra.IPs) != 0 {
		t.Errorf("不应有 IP: %v", extra.IPs)
	}
}

// sansWith 与自动探测取并集并去重：重复配置不会让 SAN 里出现两份，
// 自动探测的名字（localhost / 网卡 IP 等）一个都不能少。
func TestSansWithMergesExtras(t *testing.T) {
	base := sans()
	extra := ExtraNames{
		DNS: []string{"localhost", "adb.example.com"},
		IPs: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("203.0.113.10")},
	}
	n := sansWith(extra)

	countDNS := map[string]int{}
	for _, d := range n.dns {
		countDNS[d]++
	}
	if countDNS["localhost"] != 1 {
		t.Errorf("localhost 应恰好出现 1 次，实际 %d 次", countDNS["localhost"])
	}
	if countDNS["adb.example.com"] != 1 {
		t.Error("追加的主机名没有进入 SAN")
	}
	for _, d := range base.dns {
		if countDNS[d] != 1 {
			t.Errorf("自动探测的主机名 %s 丢失或重复（%d 次）", d, countDNS[d])
		}
	}

	countIP := map[string]int{}
	for _, ip := range n.ips {
		countIP[ip.String()]++
	}
	if countIP["127.0.0.1"] != 1 || countIP["203.0.113.10"] != 1 {
		t.Errorf("IP 去重/追加不对: %v", countIP)
	}
	for _, ip := range base.ips {
		if countIP[ip.String()] != 1 {
			t.Errorf("自动探测的 IP %s 丢失或重复", ip.String())
		}
	}
}

// Ensure 端到端：追加的名字进入叶证书 SAN；extra 不变则复用，
// extra 变化则重签叶证书但 CA 保持稳定（用户信任不作废）。
func TestEnsureIncludesExtraNames(t *testing.T) {
	dir := t.TempDir()
	extra := ExtraNames{DNS: []string{"adb.example.com"}, IPs: []net.IP{net.ParseIP("203.0.113.10")}}

	certPath, _, err := Ensure(dir, extra)
	if err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	leaf := readLeaf(t, certPath)
	got := leafNames(leaf)
	if !hasDNS(got, "adb.example.com") || !hasDNS(got, "localhost") {
		t.Errorf("SAN 主机名不全: %v", got.dns)
	}
	if !hasIP(got, "203.0.113.10") || !hasIP(got, "127.0.0.1") {
		t.Errorf("SAN IP 不全: %v", got.ips)
	}

	// 同样的 extra 再次 Ensure：复用（指纹稳定，用户无需重新信任）
	first := readLeaf(t, filepath.Join(dir, certName))
	if _, _, err := Ensure(dir, extra); err != nil {
		t.Fatalf("二次 Ensure 失败: %v", err)
	}
	if second := readLeaf(t, filepath.Join(dir, certName)); first.SerialNumber.Cmp(second.SerialNumber) != 0 {
		t.Error("extra 未变却重签了叶证书")
	}

	// extra 变化：重签叶证书，CA 不变
	caBefore := readCACert(t, dir)
	changed := ExtraNames{DNS: []string{"adb.example.com"}, IPs: []net.IP{net.ParseIP("203.0.113.11")}}
	if _, _, err := Ensure(dir, changed); err != nil {
		t.Fatalf("换 extra 后 Ensure 失败: %v", err)
	}
	resigned := readLeaf(t, filepath.Join(dir, certName))
	if resigned.SerialNumber.Cmp(first.SerialNumber) == 0 {
		t.Error("extra 变化后未重签叶证书")
	}
	if !readCACert(t, dir).Equal(caBefore) {
		t.Error("CA 被重新生成了（用户信任会作废）")
	}
	if !hasIP(leafNames(resigned), "203.0.113.11") {
		t.Error("重签后的 SAN 未包含新的 extra IP")
	}
}

func hasDNS(n certNames, dns string) bool {
	for _, d := range n.dns {
		if d == dns {
			return true
		}
	}
	return false
}

func hasIP(n certNames, ip string) bool {
	for _, x := range n.ips {
		if x.String() == ip {
			return true
		}
	}
	return false
}
