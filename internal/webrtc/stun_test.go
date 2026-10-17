package webrtc

import (
	"reflect"
	"testing"
)

func TestParseICEServers(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"空串=不配置", "", nil},
		{"off 禁用", "off", nil},
		{"none 禁用", "none", nil},
		{"裸 host:port 补 stun 前缀", "stun.cloudflare.com:3478", []string{"stun:stun.cloudflare.com:3478"}},
		{"多个+空白容忍", " stun.cloudflare.com:3478 , stun.l.google.com:19302 ",
			[]string{"stun:stun.cloudflare.com:3478", "stun:stun.l.google.com:19302"}},
		{"已带 scheme 原样保留", "stun:example.com:3478,turn:turn.example.com:3478",
			[]string{"stun:example.com:3478", "turn:turn.example.com:3478"}},
		{"turns 原样", "turns:turn.example.com:5349", []string{"turns:turn.example.com:5349"}},
		{"off 之后的条目不再收", "stun:a.com:3478,off,stun:b.com:3478", nil},
		{"空项忽略", "stun:a.com:3478,,  ,stun:b.com:3478", []string{"stun:a.com:3478", "stun:b.com:3478"}},
	}
	for _, c := range cases {
		got := ParseICEServers(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseICEServers(%q) = %v，期望 %v", c.name, c.in, got, c.want)
		}
	}
}

func NewManagerSTUNDefaultWiring(t *testing.T) {
	// 默认值（config 侧）应原样解析出两个 STUN
	m := NewManager("stun.cloudflare.com:3478,stun.l.google.com:19302")
	if len(m.iceConfiguration()) != 2 {
		t.Errorf("默认 STUN 应解析出 2 个 ICE server，得到 %d", len(m.iceConfiguration()))
	}
	// 禁用时 iceConfiguration 返回 nil（pion 仍收主机候选）
	m = NewManager("off")
	if m.iceConfiguration() != nil {
		t.Error("off 时 iceConfiguration 应为 nil")
	}
}
