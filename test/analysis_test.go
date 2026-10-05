package test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Olian04/glimt/analysis"
	"github.com/Olian04/glimt/format"
)

func rec(kv ...string) *format.Record {
	r := &format.Record{}
	for i := 0; i+1 < len(kv); i += 2 {
		r.Fields = append(r.Fields, format.Field{Key: kv[i], Val: format.Infer(kv[i+1])})
	}
	return r
}

func TestFilter(t *testing.T) {
	r := rec("level", "ERROR", "dur", "250ms", "path", "/api/users")
	cases := map[string]bool{
		"":                              true,
		"level=error":                   true,
		"level!=error":                  false,
		"dur>200":                       true,
		"dur>200ms dur<1s":              true,
		"dur>=300":                      false,
		"path=/api/*":                   true,
		"path~^/api":                    true,
		"missing=*":                     false,
		"missing!=x":                    true,
		"boom":                          false,
		"-boom":                         true,
		"/USERS$/":                      true,
		`path="/api/users" level=error`: true,
	}
	for src, want := range cases {
		f, err := analysis.ParseFilter(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if got := f.Match(r, "level=ERROR dur=250ms path=/api/users"); got != want {
			t.Errorf("%q: got %v want %v", src, got, want)
		}
	}
	if _, err := analysis.ParseFilter("dur>abc"); err == nil {
		t.Error("expected error for non-numeric comparison")
	}
	// key!=v is exactly -key=v; globs cross '/'; equality is also numeric
	for _, c := range []struct {
		src  string
		want bool
		r    *format.Record
	}{
		{"-level=info", true, rec("level", "error")},
		{"level!=info", true, rec("level", "error")},
		{"level!=info", true, rec("x", "1")},
		{"level!=info*", false, rec("level", "information")},
		{"-level=*", true, rec("x", "1")},
		{"code=200", true, rec("code", "200.0")},
		{"code!=200", false, rec("code", "200.0")},
		{"dur=1s", true, rec("dur", "1000ms")},
		{"path=/api/*", true, rec("path", "/api/users/42")},
		{"path=/a?i", true, rec("path", "/api")},
		{"path=/api", false, rec("path", "/api/users")},
		{"tags[]=b", true, rec("tags[]", "a", "tags[]", "b")},
		{"tags[]!=b", false, rec("tags[]", "a", "tags[]", "b")},
	} {
		f, err := analysis.ParseFilter(c.src)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Match(c.r, ""); got != c.want {
			t.Errorf("%s on %+v: got %v", c.src, c.r.Fields, got)
		}
	}
	if _, err := analysis.ParseFilter("+healthcheck"); err == nil || !strings.Contains(err.Error(), "write healthcheck") {
		t.Errorf("+word should be rejected with a hint, got %v", err)
	}
}

func TestReplayOnFilter(t *testing.T) {
	st := analysis.NewStore(0, nil)
	for i := 0; i < 100; i++ {
		st.Append(fmt.Sprintf("level=%s n=%d\n", []string{"info", "error"}[i%2], i))
	}
	st.Close()
	a := analysis.New(st, format.Default(nil), "")
	a.Drain()
	f, _ := analysis.ParseFilter("level=error n>=50")
	a.SetFilter(f)
	a.Drain()
	a.Read(func(r *analysis.Result) {
		if r.Format.Name() != "logfmt" || r.Matched != 25 || r.VisibleCount() != 25 {
			t.Fatalf("format=%s matched=%d visible=%d", r.Format.Name(), r.Matched, r.VisibleCount())
		}
		if n := r.Fields["n"]; n.Min != 51 || n.Max != 99 {
			t.Fatalf("stats not filtered: %v..%v", n.Min, n.Max)
		}
	})
}

func TestEvictionCountersVsWindow(t *testing.T) {
	st := analysis.NewStore(64<<10, nil) // tiny cap: most rows get evicted
	a := analysis.New(st, format.Default(nil), "")
	for i := 0; i < 5000; i++ {
		st.Append(fmt.Sprintf("level=info n=%d pad=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", i))
		if i%100 == 0 {
			a.Drain()
		}
	}
	st.Close()
	a.Drain()
	info := st.Info()
	if info.Evicted == 0 || info.Used > info.Cap || info.Lines != 5000 {
		t.Fatalf("info %+v", info)
	}
	a.Read(func(r *analysis.Result) {
		n := r.Fields["n"]
		if n.NumN != 5000 || n.Min != 0 || n.Max != 4999 { // counters cover everything
			t.Fatalf("counters: n=%d min=%v max=%v", n.NumN, n.Min, n.Max)
		}
		w := n.Window() // window covers only cached rows
		if len(w) != info.Cached || w[0].Line != st.First() {
			t.Fatalf("window len=%d cached=%d first=%d/%d", len(w), info.Cached, w[0].Line, st.First())
		}
		if q := n.Quantiles(0)[0]; q != float64(st.First()) {
			t.Fatalf("p0=%v want %d", q, st.First())
		}
		if r.VisibleCount() != info.Cached || r.VisibleLine(0) != st.First() {
			t.Fatalf("visible %d", r.VisibleCount())
		}
	})
	// a replay can only cover the cache, and says so
	f, _ := analysis.ParseFilter("n>=0")
	a.SetFilter(f)
	a.Drain()
	a.Read(func(r *analysis.Result) {
		if r.From != st.First() || r.Matched != info.Cached {
			t.Fatalf("replay from=%d matched=%d", r.From, r.Matched)
		}
	})
	if u := st.Info().Used; u > st.Info().Cap {
		t.Fatalf("charges leaked across replay: %d", u)
	}
}
