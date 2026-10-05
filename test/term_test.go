package test

import (
	"testing"

	"github.com/Olian04/glimt/term"
)

func TestWidthSliceWrap(t *testing.T) {
	s := "\x1b[31mhello\x1b[0m 世界"
	if w := term.Width(s); w != 10 {
		t.Fatalf("width=%d", w)
	}
	if got := term.Strip(term.Slice(s, 2, 5, false)); got != "llo 世"[:0]+"llo " {
		// "llo " is 4 cells; 世 (2 cells) does not fit in the 5th
		t.Fatalf("slice=%q", got)
	}
	lines := term.Wrap("\x1b[32mabcdefgh\x1b[0m", 3)
	if len(lines) != 3 || term.Strip(lines[2]) != "gh" || lines[1][:5] != "\x1b[32m" {
		t.Fatalf("wrap=%q", lines)
	}
	if term.Strip("a\rb") != "b" || term.Strip("x\x1b[2Ky") != "xy" {
		t.Fatal("strip")
	}
	if term.Truncate("abcdef", 4) != "abc…" {
		t.Fatal("truncate")
	}
}

func TestDecode(t *testing.T) {
	ks := term.Decode("j\x1b[A\x1b[5~\x1b[Z\x1b[<65;3;4M\r\x7fé")
	want := []term.KeyKind{term.KRune, term.KUp, term.KPgUp, term.KShiftTab, term.KWheelDown, term.KEnter, term.KBackspace, term.KRune}
	if len(ks) != len(want) {
		t.Fatalf("got %d keys: %+v", len(ks), ks)
	}
	for i := range want {
		if ks[i].Kind != want[i] {
			t.Errorf("key %d: %v want %v", i, ks[i].Kind, want[i])
		}
	}
	if ks[7].Rune != 'é' {
		t.Errorf("utf8 rune %q", ks[7].Rune)
	}
}
