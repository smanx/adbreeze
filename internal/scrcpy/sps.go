package scrcpy

import "encoding/binary"

// parseSPSResolution 从 H.264 SPS NAL（AnnexB，含起始码或裸 NAL 均可）
// 解析编码分辨率。旋转时编码器会重发配置包并携带新的 SPS，
// 服务端借此更新视频尺寸供触摸注入使用。
// 实现遵循 ITU-T H.264 spec 7.3.2.1.1（seq_parameter_set_rbsp）。
func parseSPSResolution(nal []byte) (w, h int, ok bool) {
	nal = stripStartCode(nal)
	if len(nal) == 0 || nal[0]&0x1F != 7 { // NAL type 7 = SPS
		return 0, 0, false
	}
	nal = stripEmulationPrevention(nal[1:]) // 去掉 NAL 头 1 字节，去掉防竞争字节

	r := &bitReader{data: nal}
	profileIDC := r.bits(8)
	_ = r.bits(8) // constraint flags + reserved
	_ = r.bits(8) // level_idc
	r.ue()        // seq_parameter_set_id

	chromaFormatIDC := uint32(1) // 默认 4:2:0（仅 High 系 profile 显式出现）
	switch profileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chromaFormatIDC = r.ue()
		if chromaFormatIDC == 3 {
			r.u1() // separate_colour_plane_flag
		}
		r.ue()           // bit_depth_luma_minus8
		r.ue()           // bit_depth_chroma_minus8
		r.u1()           // qpprime_y_zero_transform_bypass_flag
		if r.u1() == 1 { // seq_scaling_matrix_present_flag
			count := 8
			if chromaFormatIDC != 3 {
				count = 12
			}
			for i := 0; i < count; i++ {
				if r.u1() == 1 { // seq_scaling_list_present_flag[i]
					skipScalingList(r, 16)
				}
			}
		}
	}

	r.ue()          // log2_max_frame_num_minus4
	switch r.ue() { // pic_order_cnt_type
	case 0:
		r.ue() // log2_max_pic_order_cnt_lsb_minus4
	case 1:
		r.u1() // delta_pic_order_always_zero_flag
		r.se() // offset_for_non_ref_pic
		r.se() // offset_for_top_to_bottom_field
		n := r.ue()
		for i := uint32(0); i < n; i++ {
			r.se() // offset_for_ref_frame[i]
		}
	}

	r.ue() // max_num_ref_frames
	r.u1() // gaps_in_frame_num_value_allowed_flag
	widthMBs := r.ue() + 1
	heightMapUnits := r.ue() + 1
	frameMbsOnly := r.u1()
	if frameMbsOnly == 0 {
		r.u1() // mb_adaptive_frame_field_flag
	}
	r.u1() // direct_8x8_inference_flag

	var cropL, cropR, cropT, cropB uint32
	if r.u1() == 1 { // frame_cropping_flag
		cropL, cropR, cropT, cropB = r.ue(), r.ue(), r.ue(), r.ue()
	}

	w = int(widthMBs) * 16
	h = int(heightMapUnits) * 16 * (2 - int(frameMbsOnly))

	// 裁剪单元（H.264 Table 6-4 简化：ChromaArrayType 有效时按色度格式）
	unitX, unitY := 1, 2-int(frameMbsOnly)
	if chromaFormatIDC != 0 {
		switch chromaFormatIDC {
		case 1, 2:
			unitX = 2
		case 3:
			unitX = 1
		}
		unitY = unitX * (2 - int(frameMbsOnly))
	}
	w -= int(cropL+cropR) * unitX
	h -= int(cropT+cropB) * unitY

	if w <= 0 || h <= 0 || r.err {
		return 0, 0, false
	}
	return w, h, true
}

// skipScalingList 跳过 scaling_list(i, size)（se 满 128 时提前结束）。
func skipScalingList(r *bitReader, size int) {
	lastScale, nextScale := uint32(8), uint32(8)
	for j := 0; j < size; j++ {
		if nextScale != 0 {
			delta := r.se()
			// uint32 回绕等价于 mod 256 意义下的有符号加法
			nextScale = (lastScale + uint32(delta) + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}
}

// stripStartCode 去掉 AnnexB 起始码（00 00 00 01 或 00 00 01）。
func stripStartCode(b []byte) []byte {
	for i := 0; i+2 < len(b)+1 && i < 4; i++ {
		if i+2 < len(b) && b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			return b[i+3:]
		}
	}
	return b
}

// stripEmulationPrevention 去掉防竞争字节（00 00 03 → 00 00）。
func stripEmulationPrevention(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if i >= 2 && b[i-2] == 0 && b[i-1] == 0 && b[i] == 3 {
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// findNAL 在 AnnexB 码流中定位指定类型的 NAL 载荷（含 NAL 头）。
func findNAL(data []byte, nalType byte) []byte {
	for i := 0; i+3 < len(data); i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			hdr := data[i+3]
			if hdr&0x1F == nalType {
				// 找下一个起始码作为结束
				for j := i + 3; j+2 < len(data); j++ {
					if data[j] == 0 && data[j+1] == 0 && (data[j+2] == 1 || (j+3 < len(data) && data[j+2] == 0 && data[j+3] == 1)) {
						return data[i+3 : j]
					}
				}
				return data[i+3:]
			}
		}
	}
	return nil
}

// bitReader 按 H.264 规范读取位流（ue/se 为 Exp-Golomb）。
type bitReader struct {
	data []byte
	pos  uint32
	err  bool
}

func (r *bitReader) u1() uint32 {
	if r.pos >= uint32(len(r.data))*8 {
		r.err = true
		return 0
	}
	bit := (r.data[r.pos/8] >> (7 - r.pos%8)) & 1
	r.pos++
	return uint32(bit)
}

func (r *bitReader) bits(n uint32) uint32 {
	v := uint32(0)
	for i := uint32(0); i < n; i++ {
		v = v<<1 | r.u1()
	}
	return v
}

func (r *bitReader) ue() uint32 {
	zeros := uint32(0)
	for r.u1() == 0 && zeros < 32 {
		zeros++
	}
	if zeros == 0 {
		return 0
	}
	return (1 << zeros) - 1 + r.bits(zeros)
}

func (r *bitReader) se() int32 {
	k := r.ue()
	if k%2 == 1 {
		return int32((k + 1) / 2)
	}
	return -int32(k / 2)
}

var _ = binary.BigEndian // 保持导入（未来扩展用）
