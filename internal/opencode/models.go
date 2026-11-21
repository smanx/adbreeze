package opencode

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// modelsCache 缓存 opencode models 的结果：该命令会启动后台服务，代价较高。
var modelsCache struct {
	sync.Mutex
	at     time.Time
	models []string
	err    error
}

const modelsCacheTTL = 5 * time.Minute

// ListModels 返回 opencode 当前可用的全部模型（providerID/modelID 形式），
// 通过 `opencode models` 获取（权威来源，含配置文件中自定义的 provider）。
// 结果缓存 5 分钟，避免频繁启动后台服务。
func ListModels(binary string) ([]string, error) {
	modelsCache.Lock()
	defer modelsCache.Unlock()
	if modelsCache.models != nil && time.Since(modelsCache.at) < modelsCacheTTL {
		return modelsCache.models, modelsCache.err
	}

	bin := binary
	if bin == "" {
		d := Detect("")
		if !d.Installed {
			return nil, errors.New("未检测到 opencode，无法读取模型列表")
		}
		bin = d.Path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "models").CombinedOutput()
	if err != nil {
		modelsCache.models = nil
		modelsCache.err = err
		return nil, err
	}
	var models []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		m := strings.TrimSpace(line)
		if m == "" || seen[m] {
			continue
		}
		if !strings.Contains(m, "/") {
			continue // 非法模型 ID
		}
		seen[m] = true
		models = append(models, m)
	}
	sort.Strings(models)
	modelsCache.models = models
	modelsCache.err = nil
	modelsCache.at = time.Now()
	return models, nil
}

// InvalidateModels 使模型列表缓存失效（改了 provider 配置后可主动刷新）。
func InvalidateModels() {
	modelsCache.Lock()
	modelsCache.models = nil
	modelsCache.at = time.Time{}
	modelsCache.Unlock()
}
