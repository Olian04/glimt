// Package term is a tiny, dependency-free terminal layer: raw mode, key
// decoding, and ANSI-aware string measuring/cutting.
package term

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// escLen returns the byte length of the escape sequence starting at s[0]
// (which must be ESC), or 1 if it is not a recognised sequence.
func escLen(s string) int {
	if len(s) < 2 {
		return 1
	}
	switch s[1] {
	case '[': // CSI: params then final byte 0x40..0x7e
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']': // OSC: terminated by BEL or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	return 2
}

// RuneWidth is a compact approximation of wcwidth.
func RuneWidth(r rune) int {
	switch {
	case r == 0 || r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200b:
		return 0
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f64f) ||
		(r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}

// Width is the number of terminal cells s occupies (escapes ignored).
func Width(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escLen(s[i:])
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		w += RuneWidth(r)
		i += n
	}
	return w
}

// Strip removes all escape sequences and control characters (except tab).
func Strip(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 32 && c != '\t') || c == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0x1b {
			i += escLen(s[i:])
			continue
		}
		if c == '\r' { // carriage return: keep what follows (progress-bar style)
			if i+1 < len(s) {
				b.Reset()
			}
			i++
			continue
		}
		if (c < 32 && c != '\t') || c == 0x7f {
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// ExpandTabs replaces tabs with spaces to the next multiple of 4.
func ExpandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := escLen(s[i:])
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if r == '\t' {
			sp := 4 - col%4
			b.WriteString(strings.Repeat(" ", sp))
			col += sp
			continue
		}
		b.WriteRune(r)
		col += RuneWidth(r)
	}
	return b.String()
}

// Slice returns the cells [from, from+width) of s, keeping escape sequences
// so colours survive. The result is padded with spaces to exactly width
// when pad is true.
func Slice(s string, from, width int, pad bool) string {
	var b strings.Builder
	col := 0
	used := 0
	hasEsc := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := escLen(s[i:])
			b.WriteString(s[i : i+n])
			hasEsc = true
			i += n
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		rw := RuneWidth(r)
		if col < from {
			col += rw
			if col > from { // wide rune split by the left edge
				b.WriteByte(' ')
				used++
			}
			continue
		}
		if used+rw > width {
			// consume the rest only for escapes so state is closed below
			break
		}
		b.WriteRune(r)
		col += rw
		used += rw
	}
	if hasEsc {
		b.WriteString("\x1b[0m")
	}
	if pad && used < width {
		b.WriteString(strings.Repeat(" ", width-used))
	}
	return b.String()
}

// Truncate cuts s to width cells, ending with "…" if it was cut.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if Width(s) <= width {
		return s
	}
	return Slice(s, 0, width-1, false) + "…"
}

// Pad cuts or pads s to exactly width cells.
func Pad(s string, width int) string {
	w := Width(s)
	if w == width {
		return s
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return Slice(s, 0, width, true)
}

// Wrap hard-wraps s into lines of at most width cells, carrying active SGR
// styling across the breaks.
func Wrap(s string, width int) []string {
	if width <= 0 || Width(s) <= width {
		return []string{s}
	}
	var out []string
	var b strings.Builder
	active := "" // SGR sequences since the last reset
	used := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := escLen(s[i:])
			seq := s[i : i+n]
			b.WriteString(seq)
			if strings.HasSuffix(seq, "m") {
				if seq == "\x1b[0m" || seq == "\x1b[m" {
					active = ""
				} else {
					active += seq
				}
			}
			i += n
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		rw := RuneWidth(r)
		if used+rw > width {
			if active != "" {
				b.WriteString("\x1b[0m")
			}
			out = append(out, b.String())
			b.Reset()
			b.WriteString(active)
			used = 0
		}
		b.WriteRune(r)
		used += rw
		i += n
	}
	out = append(out, b.String())
	return out
}
