package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件实现对 opencode.json(c) 的"外科式"修改：只替换/插入目标字段的字节片段，
// 不整体序列化，从而保留用户已有的注释、格式与其它字段（JSONC 友好）。
// 修改前先写 .bak 备份。

// ConfigPath 返回当前用户的 opencode 全局配置文件路径（优先 .jsonc，其次 .json）。
// 不存在时返回默认应创建的路径（opencode.json）。
func ConfigPath() string {
	base := ""
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		base = x
	} else if home, err := os.UserHomeDir(); err == nil {
		base = filepath.Join(home, ".config")
	}
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, "opencode")
	for _, name := range []string{"opencode.jsonc", "opencode.json"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return filepath.Join(dir, "opencode.json")
}

// ReadModel 读取配置里的 model 字段（顶层字符串）。未配置返回空串。
func ReadModel() string {
	path := ConfigPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	start, end, ok := findTopKey(string(data), "model")
	if !ok {
		return ""
	}
	raw := strings.TrimSpace(string(data)[start:end])
	var v string
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return ""
}

// WriteModel 设置顶层 model 字段，保留其余内容。
func WriteModel(model string) error {
	if !validModel(model) {
		return errors.New("模型名格式应为 providerID/modelID")
	}
	return editConfig(func(src string) (string, error) {
		nv, _ := json.Marshal(model)
		return setTopKey(src, "model", string(nv))
	})
}

func validModel(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\r\n\"") {
		return false
	}
	// 允许 provider/model 形式，也允许单个 token（便于本地模型）
	return len(s) <= 200
}

// mcpStyle 探测配置里 MCP 的书写风格：
//   - "v2"：mcp.servers.<name>（OpenCode V2 规范）
//   - "v1"：mcp.<name>（V1 兼容写法，仍在用）
//   - "none"：尚无 mcp 配置
func mcpStyle(src string) string {
	s, e, ok := findTopKey(src, "mcp")
	if !ok {
		return "none"
	}
	oo, oc, ok := objectSpan(src, s, e)
	if !ok {
		return "none"
	}
	if _, _, found := findKeyInObject(src, oo+1, oc, "servers"); found {
		return "v2"
	}
	return "v1"
}

// MCPInstalled 判断配置里是否注册了 mobile-mcp server（兼容 V1 / V2 两种写法）。
func MCPInstalled() bool {
	path := ConfigPath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	src := string(data)
	if _, _, ok := findNestedKey(src, []string{"mcp", "servers", "mobile-mcp"}); ok {
		return true
	}
	if _, _, ok := findNestedKey(src, []string{"mcp", "mobile-mcp"}); ok {
		return true
	}
	return false
}

// RegisterMobileMCP 在 opencode 配置里注册 mobile-mcp（local stdio）。
// 按配置现有的 MCP 风格写入（V1 直接写在 mcp 下，V2 写在 mcp.servers 下），
// 已存在时覆盖为规范配置，避免旧配置缺 environment 导致能力不全。
func RegisterMobileMCP() error {
	return editConfig(func(src string) (string, error) {
		entry := `{
      "type": "local",
      "command": ["npx", "-y", "@mobilenext/mobile-mcp@latest"],
      "environment": { "MOBILEMCP_DISABLE_TELEMETRY": "1" }
    }`
		var path []string
		if mcpStyle(src) == "v1" {
			path = []string{"mcp", "mobile-mcp"}
		} else {
			path = []string{"mcp", "servers", "mobile-mcp"}
		}
		return setNestedKey(src, path, entry)
	})
}

// ReadConfig 返回配置文件原文与路径，供设置页手动编辑。
func ReadConfig() (content, path string, err error) {
	path = ConfigPath()
	if path == "" {
		return "", "", errors.New("无法确定 opencode 配置目录")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 尚未创建：返回默认骨架，用户可直接编辑保存
		return "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n", path, nil
	}
	if err != nil {
		return "", path, fmt.Errorf("读取配置失败: %w", err)
	}
	return string(data), path, nil
}

// WriteConfig 写回用户手动编辑的配置原文（先校验是合法 JSON/JSONC，再备份落盘）。
func WriteConfig(content string) error {
	path := ConfigPath()
	if path == "" {
		return errors.New("无法确定 opencode 配置目录")
	}
	if err := validateJSONC(content); err != nil {
		return fmt.Errorf("配置内容不是合法的 JSON/JSONC: %w", err)
	}
	return saveConfig(path, content)
}

// saveConfig 备份旧内容后原子写入新内容。
//
// 关键约束：备份必须是"写入前"的旧内容——否则一旦新内容有误，
// 用户就失去了唯一可回滚的副本。备份文件名带时间戳且绝不覆盖已有备份
// （与用户历史上手工备份的 opencode.json.YYYYMMDD-HHMM.bak 命名一致）。
// 备份失败时中止写入，宁可不改也不冒险丢失原配置。
func saveConfig(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	if old, err := os.ReadFile(path); err == nil {
		if _, err := writeBackup(path, old); err != nil {
			return fmt.Errorf("备份原配置失败，已中止写入: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取原配置失败: %w", err)
	}
	return writeFileAtomic(path, []byte(content))
}

// writeBackup 把旧内容写到 <path>.<YYYYMMDD-HHMM>.bak；同名已存在时追加序号。
func writeBackup(path string, old []byte) (string, error) {
	base := path + "." + time.Now().Format("20060102-1504")
	name := base + ".bak"
	for i := 2; ; i++ {
		if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s-%d.bak", base, i)
	}
	if err := os.WriteFile(name, old, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// writeFileAtomic 先写同目录临时文件再改名，避免中途失败留下半截配置。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".opencode-config-*")
	if err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}

// validateJSONC 做基本括号/字符串配平校验（比整体 JSON 解析宽松，容忍注释与尾逗号）。
func validateJSONC(s string) error {
	stack := []byte{}
	inStr, esc, lineComment, blockComment := false, false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		next := byte(0)
		if i+1 < len(s) {
			next = s[i+1]
		}
		if lineComment {
			if c == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if c == '*' && next == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '/' && next == '/' {
			lineComment = true
			i++
			continue
		}
		if c == '/' && next == '*' {
			blockComment = true
			i++
			continue
		}
		if c == '"' {
			inStr = true
			continue
		}
		switch c {
		case '{', '[':
			stack = append(stack, c)
		case '}':
			if len(stack) == 0 || stack[len(stack)-1] != '{' {
				return errors.New("有多余的 }")
			}
			stack = stack[:len(stack)-1]
		case ']':
			if len(stack) == 0 || stack[len(stack)-1] != '[' {
				return errors.New("有多余的 ]")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if inStr {
		return errors.New("字符串未闭合")
	}
	if blockComment {
		return errors.New("块注释未闭合")
	}
	if len(stack) > 0 {
		return errors.New("括号未闭合")
	}
	return nil
}

// editConfig 读取配置、应用编辑函数、写备份后落盘。
func editConfig(fn func(string) (string, error)) error {
	path := ConfigPath()
	if path == "" {
		return errors.New("无法确定 opencode 配置目录")
	}
	src := ""
	if data, err := os.ReadFile(path); err == nil {
		src = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("读取配置失败: %w", err)
	}
	if strings.TrimSpace(src) == "" {
		src = "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"
	}
	out, err := fn(src)
	if err != nil {
		return err
	}
	if err := saveConfig(path, out); err != nil {
		return err
	}
	return nil
}

// --- 纯文本 JSON 对象编辑工具（正确处理字符串、转义与嵌套深度） ---

// findTopKey 在根对象里查找 key，返回其"值"的起止字节下标。
func findTopKey(src, key string) (int, int, bool) {
	open := indexOfRootOpen(src)
	if open < 0 {
		return 0, 0, false
	}
	return findKeyInObject(src, open+1, lastRootClose(src), key)
}

// findNestedKey 按路径逐层进入对象，返回末级 key 的值范围。
func findNestedKey(src string, path []string) (int, int, bool) {
	open := indexOfRootOpen(src)
	if open < 0 {
		return 0, 0, false
	}
	lo, hi := open+1, lastRootClose(src)
	for i, key := range path {
		s, e, ok := findKeyInObject(src, lo, hi, key)
		if !ok {
			return 0, 0, false
		}
		if i == len(path)-1 {
			return s, e, true
		}
		innerOpen, innerClose, ok := objectSpan(src, s, e)
		if !ok {
			return 0, 0, false
		}
		lo, hi = innerOpen+1, innerClose
	}
	return 0, 0, false
}

func setTopKey(src, key, value string) (string, error) {
	return setNestedKey(src, []string{key}, value)
}

// setNestedKey 设置路径末端的值；缺失的中间对象/键自动创建。
func setNestedKey(src string, path []string, value string) (string, error) {
	open := indexOfRootOpen(src)
	if open < 0 {
		return "", errors.New("配置不是合法的 JSON 对象")
	}
	return setInObject(src, open+1, lastRootClose(src), path, value)
}

func setInObject(src string, lo, hi int, path []string, value string) (string, error) {
	key := path[0]
	s, e, found := findKeyInObject(src, lo, hi, key)
	last := len(path) == 1
	if found {
		if last {
			return src[:s] + value + src[e:], nil
		}
		objOpen, objClose, ok := objectSpan(src, s, e)
		if !ok {
			// 值不是对象：整体替换为按剩余路径构造的新对象
			return src[:s] + buildObject(path[1:], value) + src[e:], nil
		}
		// 递归进入内层对象；所有下标都基于同一 src，直接返回修改后的整串
		return setInObject(src, objOpen+1, objClose, path[1:], value)
	}
	// 键不存在：在对象起始处插入
	insertAt := firstNonSpace(src, lo)
	var built string
	if last {
		built = `"` + key + `": ` + value + ",\n  "
	} else {
		built = `"` + key + `": ` + buildObject(path[1:], value) + ",\n  "
	}
	return src[:insertAt] + built + src[insertAt:], nil
}

// buildObject 用剩余路径构造嵌套对象字面量，如 ["a","b"] → {"a": {"b": value}}。
func buildObject(rest []string, value string) string {
	inner := value
	for i := len(rest) - 1; i >= 0; i-- {
		if i == len(rest)-1 {
			inner = `"` + rest[i] + `": ` + inner
		} else {
			inner = `"` + rest[i] + `": {` + inner + `}`
		}
	}
	return "{\n    " + inner + "\n  }"
}

func firstNonSpace(src string, from int) int {
	for from < len(src) && (src[from] == ' ' || src[from] == '\t' || src[from] == '\r' || src[from] == '\n') {
		from++
	}
	return from
}

func indexOfRootOpen(src string) int {
	for i := 0; i < len(src); i++ {
		if src[i] == '{' {
			return i
		}
		if src[i] == '/' && i+1 < len(src) && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
		}
	}
	return -1
}

func lastRootClose(src string) int {
	depth, inStr, esc := 0, false, false
	for i := len(src) - 1; i >= 0; i-- {
		c := src[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		} else if c == '}' {
			if depth == 0 {
				return i
			}
			depth--
		} else if c == '{' {
			depth++
		}
	}
	return len(src)
}

// findKeyInObject 在 src[lo:hi]（对象内容区）中查找顶层 key 的值范围。
func findKeyInObject(src string, lo, hi int, key string) (int, int, bool) {
	depth := 0
	needle := `"` + key + `"`
	i := lo
	for i < hi {
		c := src[i]
		if c == '"' {
			// 解析完整字符串 token（含两端引号）
			open := i
			j := i + 1
			esc := false
			for j < hi {
				if esc {
					esc = false
				} else if src[j] == '\\' {
					esc = true
				} else if src[j] == '"' {
					break
				}
				j++
			}
			if j >= hi {
				return 0, 0, false
			}
			token := src[open : j+1]
			if depth == 0 && token == needle {
				// 跳过冒号与空白，定位值
				k := j + 1
				for k < hi && (src[k] == ' ' || src[k] == '\t' || src[k] == '\r' || src[k] == '\n') {
					k++
				}
				if k >= hi || src[k] != ':' {
					i = j + 1
					continue
				}
				k++
				for k < hi && (src[k] == ' ' || src[k] == '\t' || src[k] == '\r' || src[k] == '\n') {
					k++
				}
				ve := valueEnd(src, k, hi)
				return k, ve, true
			}
			i = j + 1
			continue
		}
		if c == '{' || c == '[' {
			depth++
		} else if c == '}' || c == ']' {
			depth--
		}
		i++
	}
	return 0, 0, false
}

// valueEnd 返回值（字符串、对象、数组或标量）的结束下标（不含）。
func valueEnd(src string, start, hi int) int {
	if start >= hi {
		return hi
	}
	c := src[start]
	if c == '"' {
		i := start + 1
		esc := false
		for i < hi {
			if esc {
				esc = false
			} else if src[i] == '\\' {
				esc = true
			} else if src[i] == '"' {
				return i + 1
			}
			i++
		}
		return hi
	}
	if c == '{' || c == '[' {
		depth, inStr, esc := 0, false, false
		open, close := byte('{'), byte('}')
		if c == '[' {
			open, close = '[', ']'
		}
		for i := start; i < hi; i++ {
			ch := src[i]
			if inStr {
				if esc {
					esc = false
				} else if ch == '\\' {
					esc = true
				} else if ch == '"' {
					inStr = false
				}
				continue
			}
			if ch == '"' {
				inStr = true
			} else if ch == open {
				depth++
			} else if ch == close {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
		return hi
	}
	// 标量：到逗号或行尾
	i := start
	for i < hi && src[i] != ',' && src[i] != '\n' && src[i] != '\r' && src[i] != '}' {
		i++
	}
	return i
}

// objectSpan 返回值范围指向的 { ... } 对象内部范围。
func objectSpan(src string, s, e int) (int, int, bool) {
	raw := strings.TrimSpace(src[s:e])
	if len(raw) == 0 || raw[0] != '{' {
		return 0, 0, false
	}
	// 相对偏移
	off := strings.Index(src[s:e], "{")
	open := s + off
	close := valueEnd(src, open, e)
	return open, close - 1, close > open
}
