package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"

	"adbreeze/internal/scrcpy"
)

func (s *Server) startMirror(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	// 请求体可选：
	//   audioMode: mute(默认，采集到网页并静音设备扬声器) | device(设备扬声器出声，不采集) |
	//              both(两边都出声，v5 audio_dup，需 Android 13+)
	//   bitrateMbps/shortSize/maxFps 画质参数（编码器参数在设备端，需重启流生效；0 = 用默认值）
	//   shortSize 为"短边"目标值，内部按设备物理分辨率换算成 scrcpy 的长边上限 max_size
	var req struct {
		AudioMode   string `json:"audioMode"`
		BitRateMbps int    `json:"bitrateMbps"`
		ShortSize   int    `json:"shortSize"`
		MaxFPS      int    `json:"maxFps"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	opts := scrcpy.Options{}
	switch req.AudioMode {
	case "device":
		f := false
		opts.Audio = &f
	case "both":
		opts.AudioDup = true
	}
	if req.BitRateMbps > 0 {
		opts.BitRate = req.BitRateMbps * 1_000_000
	}
	if req.ShortSize > 0 {
		opts.MaxSize = s.shortToMaxSize(serial, req.ShortSize)
	}
	if req.MaxFPS > 0 {
		opts.MaxFPS = req.MaxFPS
	}
	info, err := s.mirror.Start(serial, opts)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// shortToMaxSize 把"短边目标值"换算成 scrcpy 的 max_size（较长边上限）：
// 读设备物理分辨率求长/短比，设备旋转时编码器按当前长边自适应缩放。
// wm size 读取失败时按常见手机比例 20:9 回退。
func (s *Server) shortToMaxSize(serial string, short int) int {
	if out, err := s.mgr.Shell(serial, "wm size"); err == nil {
		if m := regexp.MustCompile(`(\d+)x(\d+)`).FindStringSubmatch(out); m != nil {
			w, _ := strconv.Atoi(m[1])
			h, _ := strconv.Atoi(m[2])
			if w > 0 && h > 0 {
				if w < h {
					w, h = h, w // w = 长边
				}
				return int(float64(short) * float64(w) / float64(h))
			}
		}
	}
	return short * 9 / 4
}

func (s *Server) stopMirror(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if err := s.mirror.Stop(serial); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "投屏已停止"})
}

func (s *Server) listMirrors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.mirror.Active())
}
