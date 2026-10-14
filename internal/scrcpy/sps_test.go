package scrcpy

import (
	"encoding/binary"
	"testing"
)

// bitWriter 按 H.264 位流语义写位（ue/se 为 Exp-Golomb），用于构造 SPS 测试样本。
type bitWriter struct {
	data []byte
	pos  uint32
}

func (w *bitWriter) u1(b uint32) {
	if w.pos%8 == 0 {
		w.data = append(w.data, 0)
	}
	if b != 0 {
		w.data[w.pos/8] |= 1 << (7 - w.pos%8)
	}
	w.pos++
}

func (w *bitWriter) bits(v uint32, n uint32) {
	for i := int(n) - 1; i >= 0; i-- {
		w.u1(v >> uint(i) & 1)
	}
}

func (w *bitWriter) ue(v uint32) {
	// k = floor(log2(v+1))：k 个 0 + 1 + k 位 (v+1-2^k)
	v1 := v + 1
	k := uint32(0)
	for (1 << (k + 1)) <= v1 {
		k++
	}
	w.bits(0, k)
	w.u1(1)
	w.bits(v1-(1<<k), k)
}

func (w *bitWriter) se(v int32) {
	if v <= 0 {
		w.ue(uint32(-2 * v))
	} else {
		w.ue(uint32(2*v - 1))
	}
}

// buildSPS 构造一个 High Profile 的 SPS NAL（AnnexB 起始码开头）。
func buildSPS(widthMBs, heightMapUnits, frameMbsOnly uint32, crop [4]uint32) []byte {
	w := &bitWriter{}
	w.bits(100, 8) // profile_idc: High
	w.bits(0, 8)   // constraint flags
	w.bits(51, 8)  // level_idc
	w.ue(0)        // seq_parameter_set_id
	w.ue(1)        // chroma_format_idc: 4:2:0
	w.ue(0)        // bit_depth_luma_minus8
	w.ue(0)        // bit_depth_chroma_minus8
	w.u1(0)        // qpprime_y_zero_transform_bypass
	w.u1(0)        // seq_scaling_matrix_present
	w.ue(8)        // log2_max_frame_num_minus4
	w.ue(0)        // pic_order_cnt_type
	w.ue(4)        // log2_max_pic_order_cnt_lsb_minus4
	w.ue(2)        // max_num_ref_frames
	w.u1(0)        // gaps_in_frame_num_value_allowed
	w.ue(widthMBs - 1)
	w.ue(heightMapUnits - 1)
	w.u1(frameMbsOnly)
	if frameMbsOnly == 0 {
		w.u1(0) // mb_adaptive_frame_field
	}
	w.u1(1) // direct_8x8_inference
	if crop[0] != 0 || crop[1] != 0 || crop[2] != 0 || crop[3] != 0 {
		w.u1(1) // frame_cropping_flag
		w.ue(crop[0])
		w.ue(crop[1])
		w.ue(crop[2])
		w.ue(crop[3])
	} else {
		w.u1(0)
	}

	nal := []byte{0x67} // NAL header: type 7
	nal = append(nal, w.data...)
	return append([]byte{0, 0, 0, 1}, nal...)
}

func TestParseSPSResolution(t *testing.T) {
	cases := []struct {
		name  string
		sps   []byte
		wantW int
		wantH int
	}{
		// 1080x2400: 68x150 MBs，宽度裁掉 8px（4:2:0 → crop 单位 2 → 4 单位）
		{"1080x2400", buildSPS(68, 150, 1, [4]uint32{0, 4, 0, 0}), 1080, 2400},
		// 1080x1920: 68x120 MBs，宽度裁 8px
		{"1080x1920", buildSPS(68, 120, 1, [4]uint32{0, 4, 0, 0}), 1080, 1920},
		// 1920x1080: 120x68 MBs，高度裁 8px（progressive 4:2:0 crop 单位 Y = 2 → 4 单位）
		{"1920x1080", buildSPS(120, 68, 1, [4]uint32{0, 0, 0, 4}), 1920, 1080},
		// 720x1280: 45x80 MBs，宽度裁 16px → 8 单位
		{"720x1280", buildSPS(46, 80, 1, [4]uint32{0, 8, 0, 0}), 720, 1280},
	}
	for _, c := range cases {
		w, h, ok := parseSPSResolution(c.sps)
		if !ok {
			t.Errorf("%s: 解析失败", c.name)
			continue
		}
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: got %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}

func TestEncodeControlMessages(t *testing.T) {
	// keycode: [0][action][keycode][repeat][meta]
	kc := EncodeKeycode(0, 4, 0, 0)
	want := []byte{0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0}
	if string(kc) != string(want) {
		t.Errorf("EncodeKeycode = %x, want %x", kc, want)
	}

	// touch: 32 字节，字段位置校验
	tc := EncodeTouch(AMotionDown, 5, 100, 200, 1080, 2400, 0xFFFF)
	if tc[0] != 2 || tc[1] != 0 {
		t.Errorf("touch type/action 错误: %x", tc[:2])
	}
	if binary.BigEndian.Uint64(tc[2:]) != 5 {
		t.Error("pointerId 错误")
	}
	if binary.BigEndian.Uint32(tc[10:]) != 100 || binary.BigEndian.Uint32(tc[14:]) != 200 {
		t.Error("坐标错误")
	}
	if binary.BigEndian.Uint16(tc[18:]) != 1080 || binary.BigEndian.Uint16(tc[20:]) != 2400 {
		t.Error("screen 尺寸错误")
	}
	if binary.BigEndian.Uint16(tc[22:]) != 0xFFFF {
		t.Error("pressure 错误")
	}
	if len(tc) != 32 {
		t.Errorf("touch 长度 = %d, want 32", len(tc))
	}

	// scroll: 21 字节，vScroll=-1 → 0x8000
	sc := EncodeScroll(10, 20, 1080, 2400, 0, -1)
	if sc[0] != 3 || len(sc) != 21 {
		t.Fatalf("scroll 帧错误: len=%d", len(sc))
	}
	if binary.BigEndian.Uint16(sc[13:]) != 0 {
		t.Error("hScroll 应为 0")
	}
	if binary.BigEndian.Uint16(sc[15:]) != 0x8000 {
		t.Errorf("vScroll(-1) = %x, want 8000", binary.BigEndian.Uint16(sc[15:]))
	}

	// rotate: 单字节
	if r := EncodeSimple(CtlTypeRotateDevice); len(r) != 1 || r[0] != 11 {
		t.Errorf("EncodeSimple 错误: %x", r)
	}

	// text
	tx, err := EncodeText("你好")
	if err != nil {
		t.Fatal(err)
	}
	if tx[0] != 1 || binary.BigEndian.Uint32(tx[1:]) != 6 {
		t.Errorf("EncodeText 错误: %x", tx)
	}
}

func TestFindNALAndStrip(t *testing.T) {
	// 两个 NAL：SPS(0x67...) + PPS(0x68...)
	data := []byte{0, 0, 0, 1, 0x67, 1, 2, 3, 0, 0, 0, 1, 0x68, 4, 5}
	sps := findNAL(data, 7)
	if len(sps) != 4 || sps[0] != 0x67 {
		t.Errorf("findNAL(SPS) = %x", sps)
	}
	pps := findNAL(data, 8)
	if len(pps) != 3 || pps[0] != 0x68 {
		t.Errorf("findNAL(PPS) = %x", pps)
	}
	if got := stripEmulationPrevention([]byte{0, 0, 3, 5, 6}); string(got) != string([]byte{0, 0, 5, 6}) {
		t.Errorf("stripEmulationPrevention = %x", got)
	}
}
