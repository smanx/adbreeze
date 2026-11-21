package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron 是标准 5 段 cron 表达式：分 时 日 月 周。
// 支持 *、*/n、a、a-b、a-b/n、逗号列表；周 0/7 均表示周日。
// 日与周同时被限制时按"或"匹配（标准 cron 语义）。
type Cron struct {
	minute map[int]bool
	hour   map[int]bool
	dom    map[int]bool
	month  map[int]bool
	dow    map[int]bool
	domAny bool
	dowAny bool
}

// ParseCron 解析 5 段表达式，失败返回带原因的错误。
func ParseCron(expr string) (*Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 需要 5 个字段（分 时 日 月 周），实际 %d 个", len(fields))
	}
	c := &Cron{}
	var err error
	if c.minute, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("分钟: %w", err)
	}
	if c.hour, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("小时: %w", err)
	}
	c.domAny = fields[2] == "*"
	if c.dom, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("日: %w", err)
	}
	if c.month, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("月: %w", err)
	}
	c.dowAny = fields[4] == "*"
	if c.dow, err = parseField(fields[4], 0, 7); err != nil {
		return nil, fmt.Errorf("周: %w", err)
	}
	if c.dow[7] {
		c.dow[0] = true
		delete(c.dow, 7)
	}
	return c, nil
}

func parseField(field string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	if field == "" {
		return nil, fmt.Errorf("字段为空")
	}
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, fmt.Errorf("空的分段")
		}
		step := 1
		body := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			body = part[:i]
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s <= 0 {
				return nil, fmt.Errorf("步长 %q 非法", part[i+1:])
			}
			step = s
		}
		lo, hi := 0, 0
		switch {
		case body == "*":
			lo, hi = min, max
		case strings.Contains(body, "-"):
			seg := strings.SplitN(body, "-", 2)
			a, err1 := strconv.Atoi(seg[0])
			b, err2 := strconv.Atoi(seg[1])
			if err1 != nil || err2 != nil || a > b {
				return nil, fmt.Errorf("范围 %q 非法", body)
			}
			lo, hi = a, b
		default:
			v, err := strconv.Atoi(body)
			if err != nil {
				return nil, fmt.Errorf("值 %q 非法", body)
			}
			lo, hi = v, v
		}
		if lo < min || hi > max {
			return nil, fmt.Errorf("值 %d-%d 超出 %d-%d", lo, hi, min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有有效取值")
	}
	return out, nil
}

// Next 返回严格晚于 after 的下一次触发时间（本地时区）。
func (c *Cron) Next(after time.Time) time.Time {
	// 从下一分钟开始按天扫描，命中日期后再枚举小时/分钟。
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, after.Location())
	limit := day.AddDate(5, 0, 0) // 最多找 5 年
	for d := day; d.Before(limit); d = d.AddDate(0, 0, 1) {
		if !c.matchDay(d) {
			continue
		}
		for h := 0; h < 24; h++ {
			if !c.hour[h] {
				continue
			}
			for m := 0; m < 60; m++ {
				if !c.minute[m] {
					continue
				}
				t := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, after.Location())
				if t.After(after) {
					return t
				}
			}
		}
	}
	return time.Time{}
}

func (c *Cron) matchDay(t time.Time) bool {
	monthOK := c.month[int(t.Month())]
	domOK := c.dom[t.Day()]
	dowOK := c.dow[int(t.Weekday())]
	if c.domAny || c.dowAny {
		return monthOK && domOK && dowOK
	}
	// 日与周都限定时按"或"匹配
	return monthOK && (domOK || dowOK)
}
