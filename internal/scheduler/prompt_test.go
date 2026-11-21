package scheduler

import (
	"strings"
	"testing"
)

func TestPromptWithDevice(t *testing.T) {
	const prompt = "take a screenshot"

	if got := promptWithDevice(prompt, "  "); got != prompt {
		t.Fatalf("empty device should keep prompt unchanged, got %q", got)
	}

	got := promptWithDevice(prompt, " 10.0.0.102:5555 ")
	for _, want := range []string{"10.0.0.102:5555", "mobile-mcp", prompt, "不要反问"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt %q missing %q", got, want)
		}
	}
	// 真正的任务必须排在最前的【任务】区块里，设备约束只能作为后置要求。
	if !strings.HasPrefix(got, "【任务】\n"+prompt) {
		t.Fatalf("task instruction should come first, got %q", got)
	}
}
