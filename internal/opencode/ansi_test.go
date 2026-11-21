package opencode

import "testing"

func TestStripANSI(t *testing.T) {
	in := "\x1b[0m\n> build · model\n\x1b[31mred\x1b[0m\r\n\x1b]0;title\x07OK"
	want := "\n> build · model\nred\r\nOK"
	if got := stripANSI(in); got != want {
		t.Fatalf("stripANSI() = %q, want %q", got, want)
	}
}
