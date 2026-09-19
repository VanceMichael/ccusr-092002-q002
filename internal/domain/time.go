package domain

import "time"

// 全部存储时间统一为带时区偏移的 ISO 8601（秒精度）。
const timeLayout = time.RFC3339

// ts 将时间格式化为 ISO 8601 字符串；零值返回空串以写入 NULL 列。
func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// pt 解析 ISO 8601 字符串；空串或无法解析时返回零值时间。
func pt(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(timeLayout, raw); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t
	}
	return time.Time{}
}

// parseLoose 宽松解析输入时间，支持 ISO 8601 与常见日期写法。
func parseLoose(raw string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
