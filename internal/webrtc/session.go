// Package webrtc 提供投屏流的 WebRTC 分发方案：把 scrcpy hub 扇出的
// 视频（H.264）与音频（Opus）帧经 pion PeerConnection 推给浏览器，
// 由浏览器原生解码渲染。scrcpy 流仍是唯一数据源，WebRTC 只是另一种订阅通道。
package webrtc

import (
	"fmt"
	"log"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"adbreeze/internal/scrcpy"
)

// Manager 管理所有活跃的 WebRTC 观看会话。
type Manager struct {
	mu   sync.Mutex
	next atomic.Int64
	alls map[string]*Session // key: serial/sessionID
}

func NewManager() *Manager {
	return &Manager{alls: make(map[string]*Session)}
}

// Session 是单个观看者的 WebRTC 会话。
type Session struct {
	id        string
	pc        *webrtc.PeerConnection
	videoSub  *scrcpy.Subscriber
	videoCap  func()
	audioSub  *scrcpy.Subscriber
	audioCap  func()
	closeOnce sync.Once
}

// Create 与浏览器完成信令（offer → answer）并开始推流。
// 对同一设备的多个观看者各自建立独立 PeerConnection（hub 天然支持多订阅扇出）。
func (m *Manager) Create(serial string, mirror *scrcpy.Manager, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if offer.Type != webrtc.SDPTypeOffer {
		return webrtc.SessionDescription{}, fmt.Errorf("信令类型应为 offer，收到 %s", offer.Type)
	}

	// 订阅投屏流（不存在则快速失败）
	vSub, vCap, vCfg, info, err := mirror.Subscribe(serial)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		vCap()
		return webrtc.SessionDescription{}, fmt.Errorf("初始化媒体引擎失败: %w", err)
	}
	// 关闭 mDNS 候选：内网直连用真实 IP，省去 mDNS 解析、加快 gathering/连通
	se := &webrtc.SettingEngine{}
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(*se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		vCap()
		return webrtc.SessionDescription{}, fmt.Errorf("创建 PeerConnection 失败: %w", err)
	}

	s := &Session{
		videoSub: vSub, videoCap: vCap,
	}

	// 视频轨：H.264（AnnexB 样本直接交给 pion 按 RFC 6184 打包）
	vTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "adbreeze")
	if err == nil {
		_, err = pc.AddTrack(vTrack)
	}
	if err != nil {
		s.close()
		return webrtc.SessionDescription{}, fmt.Errorf("创建视频轨失败: %w", err)
	}

	// 音频轨：音频采集就绪通常在投屏启动后 1~2 秒，轮询等待（不阻塞视频）。
	// 超时上限 3 秒：设备禁用音频时（如 audio_dup 需 Android 13+）SubscribeAudio
	// 永远失败，等太久会把 answer 拖到关键帧窗口之后，导致浏览器错过首个 IDR。
	var aTrack *webrtc.TrackLocalStaticSample
	aSub, aCap := waitAudio(mirror, serial, 3*time.Second)
	if aCap != nil {
		s.audioSub, s.audioCap = aSub, aCap
		aTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, Channels: 2}, "audio", "adbreeze")
		if err == nil {
			_, err = pc.AddTrack(aTrack)
		}
		if err != nil {
			aCap()
			s.audioSub, s.audioCap, aTrack = nil, nil, nil
		}
	}

	if err := pc.SetRemoteDescription(offer); err != nil {
		s.close()
		return webrtc.SessionDescription{}, fmt.Errorf("设置 offer 失败: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		s.close()
		return webrtc.SessionDescription{}, fmt.Errorf("创建 answer 失败: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		s.close()
		return webrtc.SessionDescription{}, fmt.Errorf("设置 answer 失败: %w", err)
	}

	// 非 trickle：等 ICE gathering 完成再回完整 answer（内网 host candidate 直连）
	select {
	case <-webrtc.GatheringCompletePromise(pc):
	case <-time.After(3 * time.Second):
	}

	ans := *pc.LocalDescription()
	ans.SDP = addOpusStereo(ans.SDP) // scrcpy 采集为立体声，需告知浏览器按 stereo 解码

	s.pc = pc
	s.id = fmt.Sprintf("%s/%d", serial, m.next.Add(1))
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			m.Remove(s)
		}
	})

	m.mu.Lock()
	m.alls[s.id] = s
	m.mu.Unlock()

	if s.audioSub != nil {
		go s.pumpAudio(aTrack)
	}
	go s.pumpVideo(vTrack, vCfg)
	log.Printf("[webrtc:%s] 会话已建立（%dx%d，audio=%v）", serial, info.Width, info.Height, s.audioSub != nil)
	return ans, nil
}

// waitAudio 轮询订阅音频流（采集初始化通常 1~2 秒），超时返回 nil 表示仅视频。
func waitAudio(mirror *scrcpy.Manager, serial string, timeout time.Duration) (*scrcpy.Subscriber, func()) {
	deadline := time.Now().Add(timeout)
	for {
		sub, cancel, _, err := mirror.SubscribeAudio(serial)
		if err == nil {
			return sub, cancel
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Remove 关闭并移除会话（PC 断开或服务端清理时触发，幂等）。
func (m *Manager) Remove(s *Session) {
	s.closeOnce.Do(func() {
		if s.videoCap != nil {
			s.videoCap()
		}
		if s.audioCap != nil {
			s.audioCap()
		}
		if s.pc != nil {
			_ = s.pc.Close()
		}
		m.mu.Lock()
		delete(m.alls, s.id)
		m.mu.Unlock()
	})
}

// pumpVideo 把 hub 视频帧写入 WebRTC 轨：从首个关键帧开始发（P 帧先到时丢弃），
// 关键帧前拼接缓存的 SPS/PPS 配置包；RTP 时间戳用相邻帧 PTS 差（scrcpy PTS 为微秒）。
func (s *Session) pumpVideo(track *webrtc.TrackLocalStaticSample, cachedConfig []byte) {
	var lastPTS uint64
	havePTS := false
	keySeen := false
	waitLogged := false
	for p := range s.videoSub.Chan() {
		if p.Kind == scrcpy.PacketResize {
			continue
		}
		if p.Config {
			// 设备编码器写出的 SPS level 常与实际分辨率不符（实测 Level 1.0 配 1600×720），
			// Chrome 的 WebRTC 硬解器按 SPS level 分配资源、直接拒解。改写为足够高的 level。
			// 拷贝改写，不污染其他订阅路径共享的帧数据。
			fixed := make([]byte, len(p.Data))
			copy(fixed, p.Data)
			patchSPSLevel(fixed, 0x2c) // Level 4.2：支持 1080p60
			cachedConfig = fixed
			continue
		}
		if !p.Key && !keySeen {
			// 等待首个关键帧（投屏启动时熄屏、编码器低运动场景可能延迟较久）
			if !waitLogged {
				waitLogged = true
				log.Printf("[webrtc] 等待编码器输出关键帧…")
			}
			continue
		}
		if p.Key && !keySeen {
			log.Printf("[webrtc] 收到关键帧，开始推流")
			keySeen = true
		}
		data := p.Data
		if p.Key && cachedConfig != nil {
			data = append(append(make([]byte, 0, len(cachedConfig)+len(p.Data)), cachedConfig...), p.Data...)
		}
		dur := 33 * time.Millisecond
		if havePTS && p.PTS > lastPTS {
			dur = time.Duration(p.PTS-lastPTS) * time.Microsecond
		}
		// 轨道尚未完成协商绑定时写帧会报错：丢弃并回退关键帧状态——首个 IDR 若在
		// 绑定前到达已被丢弃，后续 P 帧失去参考，必须等下一个关键帧再开始推流
		if err := track.WriteSample(media.Sample{Data: data, Duration: dur}); err != nil {
			if keySeen {
				keySeen = false
				waitLogged = false
			}
			continue
		}
		lastPTS, havePTS = p.PTS, true
	}
}

// pumpAudio 把 hub 音频帧（opus）写入 WebRTC 轨；OpusHead 配置包不属于 RTP 载荷，跳过。
//
// RTP 时间戳必须按「包内真实音频时长」推进，绝不能用设备 PTS 增量：scrcpy 的音频 PTS
// 取自设备端墙钟（AudioEncoder#regeneratePts），反映采集调度抖动（实测 4.5~41ms 乱跳，
// 设备静默后恢复时还有亚毫秒增量），而 opus 包内容时长是固定的——OPPO 实测 TOC=0xFC
// （config 31 = CELT 全频带 20ms 单帧）。用抖动的 PTS 增量推进时间戳会让浏览器 NetEQ
// 抖动缓冲持续失配，表现为音频卡顿、断续、严重失真（WS/WebCodecs 路径不按 PTS 走播所以不受影响）。
func (s *Session) pumpAudio(track *webrtc.TrackLocalStaticSample) {
	const opusFrameDur = 20 * time.Millisecond // 与设备端编码器帧长一致（TOC config 31）
	for p := range s.audioSub.Chan() {
		if p.Config {
			continue
		}
		// 轨道尚未绑定时写帧报错：丢弃继续，绑定后的帧自然正常
		if err := track.WriteSample(media.Sample{Data: p.Data, Duration: opusFrameDur}); err != nil {
			continue
		}
	}
}

func (s *Session) close() {
	if s.videoCap != nil {
		s.videoCap()
	}
	if s.audioCap != nil {
		s.audioCap()
	}
	if s.pc != nil {
		_ = s.pc.Close()
	}
}

// patchSPSLevel 在 AnnexB 配置包中定位 SPS NALU 并改写 level_idc。
func patchSPSLevel(config []byte, level byte) {
	for i := 0; i+7 < len(config); i++ {
		if config[i] == 0 && config[i+1] == 0 && config[i+2] == 0 && config[i+3] == 1 && config[i+4]&0x1f == 7 {
			config[i+7] = level // NAL header(i+4) 后：profile(i+5)、constraints(i+6)、level(i+7)
			return
		}
	}
}

// addOpusStereo 在 answer SDP 的 opus fmtp 上追加 stereo 声明。
// scrcpy 采集音频为立体声，浏览器默认按 mono 混音，需显式声明 stereo=1。
func addOpusStereo(sdp string) string {
	re := regexp.MustCompile(`(?m)^a=rtpmap:(\d+) opus/48000/2\r?$`)
	m := re.FindStringSubmatch(sdp)
	if m == nil {
		return sdp
	}
	pt := m[1]
	fmtpRe := regexp.MustCompile(`(?m)^a=fmtp:` + pt + ` ([^\r\n]+)\r?$`)
	if fmtpRe.MatchString(sdp) {
		return fmtpRe.ReplaceAllString(sdp, "a=fmtp:"+pt+" $1;stereo=1;sprop-stereo=1")
	}
	return re.ReplaceAllString(sdp, m[0]+"\r\na=fmtp:"+pt+" stereo=1;sprop-stereo=1")
}
