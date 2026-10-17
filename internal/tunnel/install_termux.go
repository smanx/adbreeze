package tunnel

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

// android/arm64 的一键安装：Cloudflare 不发布 android 平台的官方二进制，
// 改用 Termux 软件仓库的 aarch64 构建——它是 GOOS=android 的 bionic 动态
// PIE，与 APK 内 adb/adbreeze 相同的 linker64 执行模型（模拟器实测通过；
// 官方 linux/arm64 是静态 ET_EXEC，linker64 直接拒绝）。
//
// 信任模型：固定 Termux 官方仓库 + HTTPS；deb 未带校验和文件，沿用
// 「--version 冒烟 + 界面展示 SHA256」的人工核对策略（SHA 是 deb 的）。
//
// deb 结构 = ar 归档：debian-binary / control.tar.xz / data.tar.xz，
// 目标二进制在 data.tar.xz 的 usr/bin/cloudflared。

const termuxPoolURL = "https://packages.termux.dev/apt/termux-main/pool/main/c/cloudflared/"

// debNameRe 匹配 pool 目录列表里的 aarch64 包名（版本形如 2026.10.1）。
var debNameRe = regexp.MustCompile(`cloudflared_([0-9][0-9.]*)_aarch64\.deb`)

// latestTermuxDeb 从仓库目录列表 HTML 里选出最新的 aarch64 deb 文件名。
// 版本比较按「.」分段数值化，避免字典序把 2026.9 排到 2026.10 后面。
func latestTermuxDeb(listing []byte) (string, error) {
	best, bestVer := "", ""
	for _, m := range debNameRe.FindAllStringSubmatch(string(listing), -1) {
		name, ver := m[0], m[1]
		if best == "" || compareNumVer(ver, bestVer) > 0 {
			best, bestVer = name, ver
		}
	}
	if best == "" {
		return "", fmt.Errorf("Termux 仓库列表中未找到 aarch64 的 cloudflared 包")
	}
	return best, nil
}

// compareNumVer 按「.」分段数值比较两个版本号；无法数值化的段按字符串比。
func compareNumVer(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var sa, sb string
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		na, ea := strconv.Atoi(sa)
		nb, eb := strconv.Atoi(sb)
		if ea == nil && eb == nil {
			if na != nb {
				if na > nb {
					return 1
				}
				return -1
			}
			continue
		}
		if c := strings.Compare(sa, sb); c != 0 {
			return c
		}
	}
	return 0
}

// runAndroid 是 android/arm64 的安装主流程：
// 列表选包 → 下载 deb（复用进度/SHA/限流）→ 解包就位 → 赋权 → 冒烟。
func (in *Installer) runAndroid() {
	defer func() {
		in.mu.Lock()
		in.running = false
		in.status.FinishedAt = time.Now()
		in.mu.Unlock()
	}()

	binDir := filepath.Join(in.dataDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		in.fail(fmt.Errorf("创建安装目录失败: %w", err))
		return
	}
	final := filepath.Join(binDir, binName())
	tmp := final + ".download"
	defer os.Remove(tmp)

	// 1. 选最新包（目录列表很小，一次拉完）
	listing, err := in.httpGet(termuxPoolURL)
	if err != nil {
		in.fail(fmt.Errorf("获取 Termux 仓库列表失败: %w", err))
		return
	}
	name, err := latestTermuxDeb(listing)
	if err != nil {
		in.fail(err)
		return
	}

	// 2. 下载 deb（进度条 / SHA256 / 大小上限复用主流程）
	log.Printf("[tunnel] 一键安装 cloudflared（android/arm64）: %s%s", termuxPoolURL, name)
	if err := in.download(termuxPoolURL+name, tmp); err != nil {
		in.fail(err)
		return
	}

	// 3. 解 deb → data.tar.xz → usr/bin/cloudflared 就位
	in.setState(InstallVerifying)
	if err := extractDebCloudflared(tmp, final); err != nil {
		in.fail(fmt.Errorf("解包失败: %w", err))
		return
	}
	if err := os.Chmod(final, 0o700); err != nil {
		in.fail(fmt.Errorf("设置执行权限失败: %w", err))
		return
	}

	// 4. 冒烟：--version 经 linker64 跑通才算装好
	ver, err := in.smoke(final)
	if err != nil {
		in.fail(fmt.Errorf("安装后自检失败（%s 无法执行）: %w", final, err))
		return
	}

	in.mu.Lock()
	in.status.State = InstallDone
	in.status.Progress = 100
	in.status.Path = final
	in.status.Version = ver
	in.mu.Unlock()
	log.Printf("[tunnel] cloudflared 一键安装完成: %s（%s）", final, ver)
}

// androidDNSResolver：Android 上 Go 纯解析器拿不到系统 DNS——/etc/resolv.conf
// 不存在，Go 回退 [::1]:53，必然 connection refused（真机模拟器实测一致）。
// Termux 的 cloudflared 自身用 cgo 走 bionic getaddrinfo（dnsproxyd）不受影响；
// 这里只是安装器自己下载 deb 需要解析：直接向公共 DNS 查询，国内源优先，
// 依序兜底（每个查询失败自动换下一家）。
func androidDNSResolver() *net.Resolver {
	servers := []string{"223.5.5.5:53", "119.29.29.29:53", "1.1.1.1:53", "8.8.8.8:53"}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var lastErr error
			for _, s := range servers {
				d := net.Dialer{Timeout: 3 * time.Second}
				c, err := d.DialContext(ctx, network, s)
				if err == nil {
					return c, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

// androidHTTPClient 构造绕过系统 DNS 的 HTTP 客户端：解析走 androidDNSResolver，
// 逐个 IP 尝试连接；TLS 的 SNI/证书校验仍按 URL 主机名（Transport 语义不变）。
func androidHTTPClient(timeout time.Duration) *http.Client {
	resolver := androidDNSResolver()
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				if net.ParseIP(host) != nil { // 已是 IP，直接连
					return dialer.DialContext(ctx, network, addr)
				}
				addrs, err := resolver.LookupHost(ctx, host)
				if err != nil {
					return nil, fmt.Errorf("解析 %s 失败: %w", host, err)
				}
				var lastErr error
				for _, ip := range addrs {
					c, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
					if e == nil {
						return c, nil
					}
					lastErr = e
				}
				return nil, lastErr
			},
		},
	}
}

// httpGet GET 一个 URL 并返回响应体（沿用安装器的 HTTP 客户端与超时）。
func (in *Installer) httpGet(url string) ([]byte, error) {
	resp, err := in.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d（%s）", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 目录列表很小，4MB 兜底
}

// extractDebCloudflared 从 deb（ar 归档）中取出 data.tar.xz，再从 tar 里
// 找到 usr/bin/cloudflared（按基名匹配）写到 dst。
func extractDebCloudflared(debPath, dst string) error {
	f, err := os.Open(debPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// ar 归档：8B 全局头 "!<arch>\n"，随后每成员 60B 头 + 数据（2 字节对齐）。
	magic := make([]byte, 8)
	if _, err := io.ReadFull(f, magic); err != nil {
		return fmt.Errorf("不是合法的 deb（ar 归档）: %w", err)
	}
	if string(magic) != "!<arch>\n" {
		return fmt.Errorf("不是合法的 deb（ar 头不符）")
	}
	var xzData []byte
	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(f, hdr); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		// 成员名：右padding 可能是空格或 NUL（不同 ar 实现不一），GNU 风格尾缀 "/"
		name := strings.TrimSuffix(strings.TrimRight(string(hdr[0:16]), " \x00"), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return fmt.Errorf("ar 成员 %q 大小字段非法: %w", name, err)
		}
		if name == "data.tar.xz" {
			xzData = make([]byte, size)
			if _, err := io.ReadFull(f, xzData); err != nil {
				return fmt.Errorf("读取 data.tar.xz 失败: %w", err)
			}
			break
		}
		// 跳过其它成员（debian-binary / control.tar.*），2 字节对齐
		skip := size + size%2
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return err
		}
	}
	if xzData == nil {
		return fmt.Errorf("deb 内未找到 data.tar.xz（可能是非 xz 压缩的新格式）")
	}
	return extractXzMember(xzData, dst, "cloudflared")
}

// extractXzMember 从 xz 压缩的 tar 流中取出指定基名的普通文件写到 dst。
func extractXzMember(xzData []byte, dst, wantBase string) error {
	xr, err := xz.NewReader(bytes.NewReader(xzData))
	if err != nil {
		return fmt.Errorf("不是合法的 xz 流: %w", err)
	}
	tr := tar.NewReader(xr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != wantBase {
			continue
		}
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("压缩包内未找到 %s", wantBase)
}
