package test

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/Olian04/glimt/format"
)

func readSample(t *testing.T, name string) []string {
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestDetect(t *testing.T) {
	reg := format.Default(nil)
	cases := map[string]string{
		"app.ndjson": "json", "app.logfmt": "logfmt", "ping.txt": "text",
		"data.csv": "csv", "access.log": "access-log", "app.log": "leveled",
		"doc.json": "json-doc", "pods.txt": "table", "plain.txt": "text",
	}
	for file, want := range cases {
		s := readSample(t, file)
		f, score := reg.Detect(s[:min(200, len(s))])
		if f.Name() != want {
			t.Errorf("%s: got %s (%.2f), want %s", file, f.Name(), score, want)
		}
	}
}

func parseAll(f format.Format, lines []string) []format.Record {
	p := f.NewParser()
	var out []format.Record
	for i, l := range lines {
		r, _ := p.Parse(i, l)
		out = append(out, r...)
	}
	return out
}

func TestTextKeyValues(t *testing.T) {
	recs := parseAll(format.Text{}, []string{"64 bytes from 1.1.1.1: icmp_seq=3 ttl=57 time=12.1 ms", "PING x", "a+b=c"})
	if len(recs) != 3 || len(recs[0].Fields) != 3 || len(recs[1].Fields) != 0 || len(recs[2].Fields) != 0 {
		t.Fatalf("%+v", recs)
	}
	if v, _ := recs[0].Get("time"); v.Kind != format.Num || v.N != 12.1 {
		t.Fatalf("time=%+v", v)
	}
}

func TestJSONDocItems(t *testing.T) {
	lines := readSample(t, "doc.json")
	f, _ := format.Default(nil).Detect(lines)
	if n := len(parseAll(f, lines)); n != 30 {
		t.Fatalf("got %d records, want 30 items", n)
	}
	arr := strings.Split("[\n  {\"a\": 1},\n  {\"a\": 2, \"b\": \"x]\"}\n]", "\n")
	recs := parseAll(format.JSONDoc{}, arr)
	if len(recs) != 2 || recs[1].Line != 2 {
		t.Fatalf("array elements: %+v", recs)
	}
}

func TestTable(t *testing.T) {
	lines := readSample(t, "pods.txt")
	f, _ := format.Default(nil).Detect(lines)
	recs := parseAll(f, lines)
	if len(recs) != 20 {
		t.Fatalf("got %d", len(recs))
	}
	for _, r := range recs {
		if v, _ := r.Get("status"); v.S == "CrashLoopBackOff" {
			if rs, _ := r.Get("restarts"); !strings.Contains(rs.S, "ago") {
				t.Fatalf("restarts cell split wrong: %q", rs.S)
			}
		}
	}
}

func TestInfer(t *testing.T) {
	for in, want := range map[string]format.Kind{"12": format.Num, "-3.5": format.Num, "12ms": format.Num, "1.2.3": format.Str, "0xff": format.Str, "true": format.Bool, "null": format.Null, "abc": format.Str, "nan": format.Str} {
		if got := format.Infer(in).Kind; got != want {
			t.Errorf("format.Infer(%q)=%v want %v", in, got, want)
		}
	}
	if v := format.Infer("1.5s"); v.N != 1500 || v.Unit != "ms" {
		t.Errorf("duration: %+v", v)
	}
}

func TestPrettyJSONInline(t *testing.T) {
	out := format.PrettyJSON(`{"a":1,"b":{"c":[1,2]},"s":"x"}`, 80)
	if len(out) != 5 {
		t.Fatalf("lines=%d %q", len(out), out)
	}
}

func TestBestEffortEmbeddedJSON(t *testing.T) {
	out := format.BestEffort(`2026 started {"pid": 1, "image": "x"}`, 80)
	if len(out) < 3 {
		t.Fatalf("%q", out)
	}
}
