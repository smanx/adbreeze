package adb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// sync 数据块上限。32KB 在所有 adb 版本上都是安全值。
const (
	syncDataMax = 32 * 1024
	syncFailMax = 1024 // adbd 的 FAIL 消息长度上限

	// Linux 文件类型位（与设备端 st_mode 一致）
	S_IFDIR = 0x4000
	S_IFREG = 0x8000
	S_IFLNK = 0xA000
	S_IFMT  = 0xF000
)

// Push 通过 adb sync 协议将内存数据推送到设备文件（等价 adb push）。
// mode 为八进制权限字符串，如 "0644"。
//
// 注意：sync 协议所有 u32 字段均为小端（adbd 以原生 LE 结构体收发），
// 这与 smart socket 的十六进制长度前缀是两套编码，不可混用。
func (c *Client) Push(serial, remotePath string, data []byte, mode string) error {
	return c.PushStream(serial, remotePath, bytes.NewReader(data), mode)
}

// PushStream 流式推送：从 r 读取内容写入设备文件，不整体驻留内存（大文件上传用）。
func (c *Client) PushStream(serial, remotePath string, r io.Reader, mode string) error {
	conn, err := c.transport(serial)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeCmd(conn, "sync:"); err != nil {
		return err
	}
	if err := expectStatus(conn); err != nil {
		return err
	}

	// SEND: id + u32(LE) 长度 + "path,mode"
	spec := remotePath + "," + mode
	if err := writeSyncRequest(conn, "SEND", []byte(spec)); err != nil {
		return err
	}
	// DATA 分块
	buf := make([]byte, syncDataMax)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if err := writeSyncRequest(conn, "DATA", buf[:n]); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	// DONE: id + u32(LE) mtime（无长度前缀）
	done := make([]byte, 8)
	copy(done, "DONE")
	binary.LittleEndian.PutUint32(done[4:], uint32(time.Now().Unix()))
	if _, err := conn.Write(done); err != nil {
		return err
	}
	return readSyncStatus(conn)
}

// FileEntry 是 sync 目录列表中的一项。
type FileEntry struct {
	Name string `json:"name"`
	Size uint64 `json:"size"`
	Mode uint32 `json:"mode"`
	Time uint32 `json:"mtime"`
	Dir  bool   `json:"dir"`
}

// List 列出设备上的目录内容（sync LIST，等价 adb shell ls）。
func (c *Client) List(serial, remotePath string) ([]FileEntry, error) {
	conn, err := c.transport(serial)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := writeCmd(conn, "sync:"); err != nil {
		return nil, err
	}
	if err := expectStatus(conn); err != nil {
		return nil, err
	}
	if err := writeSyncRequest(conn, "LIST", []byte(remotePath)); err != nil {
		return nil, err
	}

	var out []FileEntry
	for {
		var id [4]byte
		if _, err := io.ReadFull(conn, id[:]); err != nil {
			return nil, err
		}
		switch string(id[:]) {
		case "DENT":
			var stat [16]byte
			if _, err := io.ReadFull(conn, stat[:]); err != nil {
				return nil, err
			}
			mode := binary.LittleEndian.Uint32(stat[0:4])
			size := binary.LittleEndian.Uint32(stat[4:8])
			mtime := binary.LittleEndian.Uint32(stat[8:12])
			nameLen := binary.LittleEndian.Uint32(stat[12:16])
			name := make([]byte, nameLen)
			if _, err := io.ReadFull(conn, name); err != nil {
				return nil, err
			}
			entryName := strings.TrimRight(string(name), "\x00")
			if entryName == "." || entryName == ".." { // 跳过相对目录项，避免误删/递归打包
				continue
			}
			out = append(out, FileEntry{
				Name: entryName,
				Size: uint64(size),
				Mode: mode,
				Time: mtime,
				Dir:  mode&S_IFDIR != 0,
			})
		case "DONE": // DONE = id + u32(0)，LIST 结束后无 OKAY
			if err := readDoneTail(conn); err != nil {
				return nil, err
			}
			return out, nil
		default:
			return nil, fmt.Errorf("adb sync: unexpected id %q", id)
		}
	}
}

// Pull 从设备下载文件到 w（sync RECV，等价 adb pull）。
func (c *Client) Pull(serial, remotePath string, w io.Writer) error {
	conn, err := c.transport(serial)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := writeCmd(conn, "sync:"); err != nil {
		return err
	}
	if err := expectStatus(conn); err != nil {
		return err
	}
	if err := writeSyncRequest(conn, "RECV", []byte(remotePath)); err != nil {
		return err
	}

	var id [4]byte
	for {
		if _, err := io.ReadFull(conn, id[:]); err != nil {
			return err
		}
		switch string(id[:]) {
		case "DATA":
			var lenBuf [4]byte
			if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
				return err
			}
			n := binary.LittleEndian.Uint32(lenBuf[:])
			if _, err := io.CopyN(w, conn, int64(n)); err != nil {
				return err
			}
		case "DONE":
			// DONE = id + u32(0)，与 LIST 一致：吞掉自带长度字段后即结束，
			// 多余字节留在连接中（随后即关闭，无影响）
			if err := readDoneTail(conn); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("adb sync: unexpected id %q", id)
		}
	}
}

// readDoneTail 读取 DONE 报文自带的 u32(0) 长度字段。
// LIST/RECV 的 DONE 为 8 字节（id + 零长度），其后没有 OKAY 终态。
func readDoneTail(conn net.Conn) error {
	var tail [4]byte
	_, err := io.ReadFull(conn, tail[:])
	return err
}

// writeSyncRequest 写入 sync 请求：4 字节 id + u32 小端长度 + 载荷。
func writeSyncRequest(conn net.Conn, id string, payload []byte) error {
	buf := make([]byte, 8, 8+len(payload))
	copy(buf, id)
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(payload)))
	buf = append(buf, payload...)
	_, err := conn.Write(buf)
	return err
}

// readSyncStatus 读取 OKAY / FAIL + message（长度为小端）。
func readSyncStatus(conn net.Conn) error {
	var id [4]byte
	if _, err := io.ReadFull(conn, id[:]); err != nil {
		return err
	}
	switch string(id[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		var lenBuf [4]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			return err
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])
		if n > syncFailMax { // 防御异常长度，避免按错误字节序分配巨量内存
			n = syncFailMax
		}
		msg := make([]byte, n)
		read, _ := io.ReadFull(conn, msg)
		return fmt.Errorf("adb sync: %s", msg[:read])
	default:
		return fmt.Errorf("adb sync: bad status %q", id)
	}
}
