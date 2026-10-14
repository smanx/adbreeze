package scrcpy

import (
	"encoding/binary"
	"fmt"
)

// ControlMessage 类型常量与线格式（scrcpy v2.4 ControlMessage.java，
// 所有多字节字段为大端）。
const (
	CtlTypeInjectKeycode       = 0
	CtlTypeInjectText          = 1
	CtlTypeInjectTouch         = 2
	CtlTypeInjectScroll        = 3
	CtlTypeBackOrScreenOn      = 4
	CtlTypeExpandNotifPanel    = 5
	CtlTypeExpandSettingsPanel = 6
	CtlTypeCollapsePanels      = 7
	CtlTypeGetClipboard        = 8
	CtlTypeSetClipboard        = 9
	CtlTypeSetScreenPowerMode  = 10
	CtlTypeRotateDevice        = 11
)

// Android KeyEvent 动作与 meta 状态。
const (
	AKeyActionDown = 0
	AKeyActionUp   = 1

	AMetaShiftOn = 1
	AMetaAltOn   = 2
	AMetaCtrlOn  = 0x1000
)

// Android MotionEvent 动作。
const (
	AMotionDown = 0
	AMotionUp   = 1
	AMotionMove = 2
)

// injectTextMaxLen 是 INJECT_TEXT 的文本字节上限（scrcpy 限制 300）。
const injectTextMaxLen = 300

// EncodeKeycode 编码 INJECT_KEYCODE:
// [type][action 1B][keycode 4B][repeat 4B][metaState 4B]
func EncodeKeycode(action byte, keycode, repeat, metaState uint32) []byte {
	buf := make([]byte, 14)
	buf[0] = CtlTypeInjectKeycode
	buf[1] = action
	binary.BigEndian.PutUint32(buf[2:], keycode)
	binary.BigEndian.PutUint32(buf[6:], repeat)
	binary.BigEndian.PutUint32(buf[10:], metaState)
	return buf
}

// EncodeText 编码 INJECT_TEXT: [type][len 4B][utf8]
func EncodeText(text string) ([]byte, error) {
	data := []byte(text)
	if len(data) > injectTextMaxLen {
		return nil, fmt.Errorf("text too long: %d bytes (max %d)", len(data), injectTextMaxLen)
	}
	buf := make([]byte, 5+len(data))
	buf[0] = CtlTypeInjectText
	binary.BigEndian.PutUint32(buf[1:], uint32(len(data)))
	copy(buf[5:], data)
	return buf, nil
}

// EncodeTouch 编码 INJECT_TOUCH_EVENT:
// [type][action 1B][pointerId 8B][x 4B][y 4B][screenW 2B][screenH 2B][pressure 2B][actionButton 4B][buttons 4B]
// 坐标为视频像素坐标；screenW/H 必须与服务端当前视频尺寸一致。
// pressure 为 u16 定点数（0 = 抬起，0xFFFF = 最大）。
func EncodeTouch(action byte, pointerId uint64, x, y int, screenW, screenH uint16, pressure uint16) []byte {
	buf := make([]byte, 32)
	buf[0] = CtlTypeInjectTouch
	buf[1] = action
	binary.BigEndian.PutUint64(buf[2:], pointerId)
	binary.BigEndian.PutUint32(buf[10:], uint32(x))
	binary.BigEndian.PutUint32(buf[14:], uint32(y))
	binary.BigEndian.PutUint16(buf[18:], screenW)
	binary.BigEndian.PutUint16(buf[20:], screenH)
	binary.BigEndian.PutUint16(buf[22:], pressure)
	binary.BigEndian.PutUint32(buf[24:], 1) // actionButton: primary
	binary.BigEndian.PutUint32(buf[28:], 1) // buttons: primary
	return buf
}

// EncodeScroll 编码 INJECT_SCROLL_EVENT:
// [type][x 4B][y 4B][screenW 2B][screenH 2B][hScroll 2B][vScroll 2B][buttons 4B]
// 滚动量为 i16 定点数（1.0 = 0x7FFF），正值向上/向右。
func EncodeScroll(x, y int, screenW, screenH uint16, hScroll, vScroll float32) []byte {
	buf := make([]byte, 21)
	buf[0] = CtlTypeInjectScroll
	binary.BigEndian.PutUint32(buf[1:], uint32(x))
	binary.BigEndian.PutUint32(buf[5:], uint32(y))
	binary.BigEndian.PutUint16(buf[9:], screenW)
	binary.BigEndian.PutUint16(buf[11:], screenH)
	binary.BigEndian.PutUint16(buf[13:], f32ToI16Fixed(hScroll))
	binary.BigEndian.PutUint16(buf[15:], f32ToI16Fixed(vScroll))
	binary.BigEndian.PutUint32(buf[17:], 0)
	return buf
}

// EncodeSetClipboard 编码 SET_CLIPBOARD:
// [type][sequence 8B][paste 1B][len 4B][utf8]
func EncodeSetClipboard(text string, paste bool) ([]byte, error) {
	data := []byte(text)
	if len(data) > injectTextMaxLen {
		return nil, fmt.Errorf("text too long: %d bytes (max %d)", len(data), injectTextMaxLen)
	}
	buf := make([]byte, 14+len(data))
	buf[0] = CtlTypeSetClipboard
	binary.BigEndian.PutUint64(buf[1:], 0) // sequence=0：不等待 ACK
	if paste {
		buf[9] = 1
	}
	binary.BigEndian.PutUint32(buf[10:], uint32(len(data)))
	copy(buf[14:], data)
	return buf, nil
}

// EncodeGetClipboard 编码 GET_CLIPBOARD: [type][copyKey 1B]
func EncodeGetClipboard(copyKey byte) []byte {
	return []byte{CtlTypeGetClipboard, copyKey}
}

// EncodeSimple 编码无载荷消息（旋转屏幕、展开/收起面板等）。
func EncodeSimple(msgType byte) []byte {
	return []byte{msgType}
}

// EncodeKeyTap 编码一次按键点击（down + up 成对），用于返回/主页等。
func EncodeKeyTap(keycode, metaState uint32) [][]byte {
	return [][]byte{
		EncodeKeycode(AKeyActionDown, keycode, 0, metaState),
		EncodeKeycode(AKeyActionUp, keycode, 0, metaState),
	}
}

// f32ToI16Fixed 把 float 转为 scrcpy 的 i16 定点数（v/32768，0x7FFF 表示 1.0）。
func f32ToI16Fixed(f float32) uint16 {
	if f >= 1 {
		return 0x7FFF
	}
	if f <= -1 {
		return 0x8000 // -32768
	}
	v := int16(f * 32768)
	if f > 0 && v == 0 {
		v = 1 // 避免小值被舍入为 0
	}
	if f < 0 && v == 0 {
		v = -1
	}
	return uint16(v)
}

// 常用 Android keycode（AKEYCODE_*）。
const (
	KeycodeVolumeUp  = 24
	KeycodeVolumeDn  = 25
	KeycodePower     = 26
	KeycodeHome      = 3
	KeycodeBack      = 4
	KeycodeMenu      = 82
	KeycodeEnter     = 66
	KeycodeDel       = 67
	KeycodeTab       = 61
	KeycodeEsc       = 111
	KeycodeDpadUp    = 19
	KeycodeDpadDown  = 20
	KeycodeDpadLeft  = 21
	KeycodeDpadRight = 22
)
