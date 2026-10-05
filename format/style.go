package format

import (
	"strconv"
	"strings"
)

// Palette (xterm-256). One place, so every view colours things the same way.
const (
	CKey    = 75
	CStr    = 114
	CNum    = 221
	CBool   = 176
	CNull   = 244
	CPunct  = 245
	CTime   = 244
	CIP     = 110
	CURL    = 74
	CError  = 203
	CWarn   = 214
	CInfo   = 78
	CDebug  = 111
	CAccent = 141
)

// C colours s with a 256-colour foreground.
func C(code int, s string) string {
	if s == "" {
		return ""
	}
	return "\x1b[38;5;" + strconv.Itoa(code) + "m" + s + "\x1b[0m"
}

// Bold, Dim and friends.
func Bold(s string) string      { return "\x1b[1m" + s + "\x1b[0m" }
func Dim(s string) string       { return "\x1b[2m" + s + "\x1b[0m" }
func Underline(s string) string { return "\x1b[4m" + s + "\x1b[0m" }
func Inverse(s string) string   { return "\x1b[7m" + s + "\x1b[0m" }

// LevelColor maps a log level word to a colour.
func LevelColor(v string) (int, bool) {
	switch strings.ToLower(strings.Trim(v, "[]<>: ")) {
	case "error", "err", "fatal", "crit", "critical", "panic", "alert", "emerg", "e", "f", "50", "60":
		return CError, true
	case "warn", "warning", "w", "40":
		return CWarn, true
	case "info", "notice", "i", "information", "30":
		return CInfo, true
	case "debug", "dbg", "d", "20":
		return CDebug, true
	case "trace", "t", "verbose", "10":
		return CNull, true
	}
	return 0, false
}

// NormLevel returns a canonical level name, or "".
func NormLevel(v string) string {
	c, ok := LevelColor(v)
	if !ok {
		return ""
	}
	switch c {
	case CError:
		return "error"
	case CWarn:
		return "warn"
	case CInfo:
		return "info"
	case CDebug:
		return "debug"
	}
	return "trace"
}

func isKey(key string, names ...string) bool {
	k := strings.ToLower(key)
	if i := strings.LastIndexByte(k, '.'); i >= 0 {
		k = k[i+1:]
	}
	for _, n := range names {
		if k == n {
			return true
		}
	}
	return false
}

// ColorValue colours a field value using both its type and its key's
// conventional meaning (levels, timestamps, HTTP status...).
func ColorValue(key string, v Value, text string) string {
	switch {
	case isKey(key, "level", "lvl", "severity", "loglevel", "levelname", "@l"):
		if c, ok := LevelColor(v.S); ok {
			return Bold(C(c, text))
		}
	case v.Kind != Num && isKey(key, "ts", "time", "timestamp", "@timestamp", "t", "date", "datetime", "@t"):
		return C(CTime, text)
	case isKey(key, "status", "status_code", "code", "statuscode") && v.Kind == Num:
		switch {
		case v.N >= 500:
			return C(CError, text)
		case v.N >= 400:
			return C(CWarn, text)
		case v.N >= 300:
			return C(CKey, text)
		case v.N >= 200:
			return C(CInfo, text)
		}
	case v.Kind == Str && isKey(key, "status", "state", "phase", "result", "outcome", "health"):
		switch strings.ToLower(v.S) {
		case "ok", "running", "ready", "succeeded", "success", "healthy", "active", "up", "pass", "passed", "true", "completed", "complete", "bound":
			return C(CInfo, text)
		case "pending", "containercreating", "waiting", "unknown", "starting", "degraded", "terminating", "warn", "skipped":
			return C(CWarn, text)
		case "lost", "error", "failed", "failure", "crashloopbackoff", "imagepullbackoff", "errimagepull", "oomkilled", "evicted", "unhealthy", "down", "fail", "dead", "exited", "false":
			return Bold(C(CError, text))
		}
	case isKey(key, "msg", "message", "event", "text", "_text"):
		return "\x1b[97m" + text + "\x1b[0m"
	case isKey(key, "err", "error", "exception", "stack", "stacktrace"):
		return C(CError, text)
	}
	switch v.Kind {
	case Num:
		return C(CNum, text)
	case Bool:
		return C(CBool, text)
	case Null:
		return C(CNull, text)
	}
	return C(CStr, text)
}
