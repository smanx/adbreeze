package scheduler

import (
	"testing"
	"time"
)

func TestParseCronBasic(t *testing.T) {
	tests := []struct {
		expr string
		ok   bool
	}{
		{"*/5 * * * *", true},
		{"0 9 * * 1-5", true},
		{"30 14 1,15 * *", true},
		{"0 0 1 1 *", true},
		{"* * * *", false},
		{"60 * * * *", false},
		{"0 24 * * *", false},
		{"0 0 0 * *", false}, // 日从 1 开始
		{"*/0 * * * *", false},
		{"a * * * *", false},
	}
	for _, tt := range tests {
		_, err := ParseCron(tt.expr)
		if (err == nil) != tt.ok {
			t.Errorf("ParseCron(%q): ok=%v, err=%v", tt.expr, tt.ok, err)
		}
	}
}

func TestCronNext(t *testing.T) {
	// 每天 09:00
	c, err := ParseCron("0 9 * * *")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 11, 21, 8, 0, 0, 0, time.Local)
	next := c.Next(after)
	if next.Hour() != 9 || next.Minute() != 0 || next.Day() != 21 {
		t.Fatalf("expected 2026-11-21 09:00, got %v", next)
	}
	after2 := time.Date(2026, 11, 21, 9, 0, 0, 0, time.Local)
	next2 := c.Next(after2)
	if next2.Day() != 22 || next2.Hour() != 9 {
		t.Fatalf("expected next day 09:00, got %v", next2)
	}
}

func TestCronEvery5Min(t *testing.T) {
	c, err := ParseCron("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 11, 21, 10, 3, 0, 0, time.Local)
	next := c.Next(after)
	if next.Minute() != 5 {
		t.Fatalf("expected minute 5, got %v", next)
	}
}
