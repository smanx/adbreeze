package tunnel

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestLatestTermuxDeb(t *testing.T) {
	listing := []byte(`
<a href="cloudflared_2026.9.0_aarch64.deb">cloudflared_2026.9.0_aarch64.deb</a>
<a href="cloudflared_2026.10.1_aarch64.deb">cloudflared_2026.10.1_aarch64.deb</a>
<a href="cloudflared_2026.10.1_arm.deb">cloudflared_2026.10.1_arm.deb</a>
<a href="cloudflared_2025.1.1_aarch64.deb">cloudflared_2025.1.1_aarch64.deb</a>
`)
	got, err := latestTermuxDeb(listing)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "cloudflared_2026.10.1_aarch64.deb" {
		t.Fatalf("got %q", got)
	}
	if _, err := latestTermuxDeb([]byte("<html>empty</html>")); err == nil {
		t.Fatal("空列表应报错")
	}
}

func TestCompareNumVer(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026.10.1", "2026.9.0", 1},  // 数值比较，非字典序
		{"2026.9", "2026.10", -1},     // 同上
		{"2026.10.1", "2026.10.1", 0}, // 相等
		{"2026.10.1", "2026.10", 1},   // 多段者大
		{"2026.10.1", "2027.1.0", -1}, // 主版本
	}
	for _, c := range cases {
		if got := compareNumVer(c.a, c.b); got != c.want {
			t.Errorf("compareNumVer(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// 构造一个最小 deb（ar 头 + data.tar.xz 内含 usr/bin/cloudflared）验证解包链路。
func TestExtractDebCloudflared(t *testing.T) {
	payload := []byte("fake-cloudflared-elf")
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: "./usr/bin/cloudflared", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	var xzBuf bytes.Buffer
	xw, err := xz.NewWriter(&xzBuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	xw.Close()

	// ar 归档拼装：debian-binary + control.tar.xz（占位）+ data.tar.xz
	member := func(name string, data []byte) []byte {
		var hdr [60]byte
		copy(hdr[0:16], name+"/")
		copy(hdr[48:58], fmtInt(len(data)))
		copy(hdr[58:60], "`\n")
		out := append([]byte{}, hdr[:]...)
		out = append(out, data...)
		if len(data)%2 == 1 {
			out = append(out, '\n') // 2 字节对齐填充
		}
		return out
	}
	var ar bytes.Buffer
	ar.WriteString("!<arch>\n")
	ar.Write(member("debian-binary", []byte("2.0\n")))
	ar.Write(member("control.tar.xz", []byte("stub")))
	ar.Write(member("data.tar.xz", xzBuf.Bytes()))

	deb := filepath.Join(t.TempDir(), "cloudflared.deb")
	dst := filepath.Join(t.TempDir(), "cloudflared")
	if err := os.WriteFile(deb, ar.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractDebCloudflared(deb, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("解包内容不匹配: %q", got)
	}
	if !strings.Contains(string(got), "fake") {
		t.Fatal("unreachable")
	}
}

func fmtInt(n int) string {
	s := []byte(strings.Repeat(" ", 10))
	num := []byte(itoa(n))
	copy(s[10-len(num):], num)
	return string(s)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
