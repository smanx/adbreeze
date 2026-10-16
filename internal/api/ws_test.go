package api

import (
	"encoding/binary"
	"testing"
)

// 视频帧头布局契约：[1B flags][8B PTS BE][8B RecvWall BE][8B SendWall BE][payload]。
// 前端 controller.ts 按同一布局解析（偏移 0/1/9/17，载荷自 25 起）——任一侧改动
// 都会让投屏画面/延迟统计静默错位，故用测试锁死。
func TestEncodeVideoFrameLayout(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	frame := encodeVideoFrame(0x03, 0x0102030405060708, 0x1112131415161718, 0x2122232425262728, payload)

	if len(frame) != 25+len(payload) {
		t.Fatalf("帧长 = %d，期望 %d", len(frame), 25+len(payload))
	}
	if frame[0] != 0x03 {
		t.Errorf("flags = %#x，期望 0x03（config|key）", frame[0])
	}
	if got := binary.BigEndian.Uint64(frame[1:9]); got != 0x0102030405060708 {
		t.Errorf("PTS = %#x，期望 0x0102030405060708", got)
	}
	if got := binary.BigEndian.Uint64(frame[9:17]); got != 0x1112131415161718 {
		t.Errorf("RecvWall = %#x，期望 0x1112131415161718", got)
	}
	if got := binary.BigEndian.Uint64(frame[17:25]); got != 0x2122232425262728 {
		t.Errorf("SendWall = %#x，期望 0x2122232425262728", got)
	}
	for i, b := range payload {
		if frame[25+i] != b {
			t.Errorf("payload[%d] = %#x，期望 %#x", i, frame[25+i], b)
		}
	}
}

// flags 只取低 2 位语义：bit0=config、bit1=key，其余位必须为 0（前端按位与解读）
func TestEncodeVideoFrameFlags(t *testing.T) {
	cases := []struct {
		config, key bool
		want        byte
	}{
		{false, false, 0x00},
		{true, false, 0x01},
		{false, true, 0x02},
		{true, true, 0x03},
	}
	for _, c := range cases {
		var flags byte
		if c.config {
			flags |= 1
		}
		if c.key {
			flags |= 2
		}
		if flags != c.want {
			t.Errorf("flags(config=%v,key=%v) = %#x，期望 %#x", c.config, c.key, flags, c.want)
		}
		if frame := encodeVideoFrame(flags, 0, 0, 0, nil); frame[0] != c.want {
			t.Errorf("编码后 flags = %#x，期望 %#x", frame[0], c.want)
		}
	}
}

// 空载荷（配置包补发前的边界）：帧体恰为 25B 头
func TestEncodeVideoFrameEmptyPayload(t *testing.T) {
	frame := encodeVideoFrame(0x01, 42, 42, 42, nil)
	if len(frame) != 25 {
		t.Fatalf("空载荷帧长 = %d，期望 25", len(frame))
	}
}
