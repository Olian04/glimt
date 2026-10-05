package term

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"
)

type winsize struct{ Row, Col, X, Y uint16 }

// Terminal is the controlling tty, opened directly so stdin can be a pipe.
type Terminal struct {
	f      *os.File
	out    *bufio.Writer
	old    syscall.Termios
	prev   []string
	Resize chan struct{}
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// Open puts /dev/tty into raw mode and switches to the alternate screen.
func Open() (*Terminal, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("no controlling terminal: %w", err)
	}
	t := &Terminal{f: f, out: bufio.NewWriterSize(f, 1<<16), Resize: make(chan struct{}, 1)}
	if err := ioctl(f.Fd(), ioctlGet, unsafe.Pointer(&t.old)); err != nil {
		f.Close()
		return nil, err
	}
	raw := t.old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctl(f.Fd(), ioctlSet, unsafe.Pointer(&raw)); err != nil {
		f.Close()
		return nil, err
	}
	// alt screen, hide cursor, enable SGR mouse (wheel)
	t.out.WriteString("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[2J")
	t.out.Flush()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		for range sig {
			select {
			case t.Resize <- struct{}{}:
			default:
			}
		}
	}()
	return t, nil
}

// Close restores the terminal.
func (t *Terminal) Close() {
	t.out.WriteString("\x1b[?1006l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l")
	t.out.Flush()
	ioctl(t.f.Fd(), ioctlSet, unsafe.Pointer(&t.old))
	t.f.Close()
}

// Size returns columns and rows.
func (t *Terminal) Size() (int, int) {
	var ws winsize
	if err := ioctl(t.f.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 {
		return 80, 24
	}
	return int(ws.Col), int(ws.Row)
}

// Draw renders a frame, rewriting only rows that changed. Each row must
// already be exactly the screen width.
func (t *Terminal) Draw(frame []string, force bool) {
	if force || len(t.prev) != len(frame) {
		t.prev = make([]string, len(frame))
		t.out.WriteString("\x1b[0m\x1b[2J")
		for i := range t.prev {
			t.prev[i] = "\x00"
		}
	}
	for i, row := range frame {
		if t.prev[i] == row {
			continue
		}
		fmt.Fprintf(t.out, "\x1b[%d;1H\x1b[0m%s\x1b[0m", i+1, row)
		t.prev[i] = row
	}
	t.out.Flush()
}

// Keys decodes input into a channel of keys.
func (t *Terminal) Keys() <-chan Key {
	ch := make(chan Key, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := t.f.Read(buf)
			if err != nil {
				close(ch)
				return
			}
			for _, k := range Decode(string(buf[:n])) {
				ch <- k
			}
		}
	}()
	return ch
}

// KeyKind enumerates the keys the UI cares about.
type KeyKind int

const (
	KRune KeyKind = iota
	KEnter
	KEsc
	KBackspace
	KTab
	KShiftTab
	KUp
	KDown
	KLeft
	KRight
	KPgUp
	KPgDn
	KHome
	KEnd
	KCtrlC
	KCtrlD
	KCtrlU
	KCtrlW
	KWheelUp
	KWheelDown
	KUnknown
)

// Key is one decoded keypress.
type Key struct {
	Kind KeyKind
	Rune rune
}

var csi = map[string]KeyKind{
	"A": KUp, "B": KDown, "C": KRight, "D": KLeft, "H": KHome, "F": KEnd,
	"Z": KShiftTab, "1~": KHome, "4~": KEnd, "7~": KHome, "8~": KEnd,
	"5~": KPgUp, "6~": KPgDn,
}

// Decode turns raw terminal input (UTF-8, CSI/SS3 escapes, SGR mouse reports)
// into keys.
func Decode(s string) []Key {
	var keys []Key
	for len(s) > 0 {
		c := s[0]
		switch {
		case c == 0x1b:
			if len(s) == 1 {
				keys = append(keys, Key{Kind: KEsc})
				s = ""
				continue
			}
			if s[1] == '[' || s[1] == 'O' {
				n := escLen("\x1b[" + s[2:])
				body := s[2:n]
				s = s[n:]
				if strings.HasPrefix(body, "<") { // SGR mouse: <b;x;yM
					var b, x, y int
					fmt.Sscanf(body[1:], "%d;%d;%d", &b, &x, &y)
					switch b {
					case 64:
						keys = append(keys, Key{Kind: KWheelUp})
					case 65:
						keys = append(keys, Key{Kind: KWheelDown})
					}
					continue
				}
				// strip modifier params like "1;5A"
				if i := strings.LastIndexByte(body, ';'); i >= 0 && len(body) > i+1 {
					body = body[i+1:]
					if len(body) > 1 && body[0] >= '0' && body[0] <= '9' {
						body = body[1:]
					}
				}
				if k, ok := csi[body]; ok {
					keys = append(keys, Key{Kind: k})
				}
				continue
			}
			keys = append(keys, Key{Kind: KEsc}) // alt+key: treat as esc
			s = s[1:]
		case c == '\r' || c == '\n':
			keys = append(keys, Key{Kind: KEnter})
			s = s[1:]
		case c == 0x7f || c == 0x08:
			keys = append(keys, Key{Kind: KBackspace})
			s = s[1:]
		case c == '\t':
			keys = append(keys, Key{Kind: KTab})
			s = s[1:]
		case c == 0x03:
			keys = append(keys, Key{Kind: KCtrlC})
			s = s[1:]
		case c == 0x04:
			keys = append(keys, Key{Kind: KCtrlD})
			s = s[1:]
		case c == 0x15:
			keys = append(keys, Key{Kind: KCtrlU})
			s = s[1:]
		case c == 0x17:
			keys = append(keys, Key{Kind: KCtrlW})
			s = s[1:]
		case c < 32:
			s = s[1:]
		default:
			r := []rune(s[:utf8Len(s)])
			keys = append(keys, Key{Kind: KRune, Rune: r[0]})
			s = s[utf8Len(s):]
		}
	}
	return keys
}

func utf8Len(s string) int {
	c := s[0]
	n := 1
	switch {
	case c >= 0xf0:
		n = 4
	case c >= 0xe0:
		n = 3
	case c >= 0xc0:
		n = 2
	}
	if n > len(s) {
		n = len(s)
	}
	return n
}
