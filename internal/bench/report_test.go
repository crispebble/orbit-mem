package bench_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/crispebble/orbit-mem/internal/bench"
)

const reportCases = `
 {"id":"c1","session":"s1","prompt":"p1","expect":["a"]},
 {"id":"c2","session":"s1","tool":"t2","expect":["b"],"allow":["c"]},
 {"id":"c3","session":"s2","prompt":"p3"},
 {"id":"c4","session":"s3","kind":"subagent","task":"T4","expect":["a"]},
 {"id":"c5","kind":"duplicate","save":{"kind":"fact","title":"t5"},"expect":["d"]}`

func fakeScript() script {
	return script{
		"start:s1": {[]string{"a"}, 30},
		"req:p1":   {[]string{"a", "b"}, 100},
		"tool:t2":  {[]string{"c"}, 40},
		"sub:T4":   {[]string{"a"}, 55},
	}
}

func TestReport는_같은_입력에서_바이트까지_같다(t *testing.T) {
	k := keyed{sim: map[string][]string{"t5": {"d"}}}
	r1, _ := runFake(t, reportCases, fakeScript(), k)
	r2, _ := runFake(t, reportCases, fakeScript(), k)
	j1, err := r1.JSON()
	if err != nil {
		t.Fatal(err)
	}
	j2, _ := r2.JSON()
	if string(j1) != string(j2) {
		t.Errorf("JSON differs between runs\n%s\n%s", j1, j2)
	}
	if r1.Text(bench.PartAll, tagWording{}) != r2.Text(bench.PartAll, tagWording{}) {
		t.Error("text differs between runs")
	}
}

func TestReport_JSON은_학습_판정_전체와_사례별_보인_id를_낸다(t *testing.T) {
	r, _ := runFake(t, reportCases, fakeScript(), keyed{sim: map[string][]string{"t5": {"d"}}})
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Cases int `json:"cases"`
		Split struct {
			Units, TrainUnits, TestUnits int
		} `json:"split"`
		Runs []struct {
			Name     string          `json:"name"`
			Train    json.RawMessage `json:"train"`
			Test     json.RawMessage `json:"test"`
			All      json.RawMessage `json:"all"`
			Sessions []struct {
				Session string   `json:"session"`
				IDs     []string `json:"ids"`
				Chars   int      `json:"chars"`
			} `json:"sessions"`
			Cases []struct {
				ID      string `json:"id"`
				Part    string `json:"part"`
				Request *struct {
					IDs   []string `json:"ids"`
					Chars int      `json:"chars"`
				} `json:"request"`
				Tool *struct {
					IDs []string `json:"ids"`
				} `json:"tool"`
			} `json:"cases"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if v.Cases != 5 || len(v.Runs) != 1 || v.Runs[0].Name != "fake" {
		t.Fatalf("%s", b)
	}
	run := v.Runs[0]
	if len(run.Train) == 0 || len(run.Test) == 0 || len(run.All) == 0 {
		t.Error("train, test and all are all there")
	}
	if len(run.Cases) != 5 || run.Cases[0].ID != "c1" || run.Cases[0].Request == nil || len(run.Cases[0].Request.IDs) != 2 || run.Cases[0].Request.Chars != 100 {
		t.Errorf("cases %+v", run.Cases)
	}
	if run.Cases[1].Tool == nil || run.Cases[1].Request != nil {
		t.Errorf("a tool case has only the tool step: %+v", run.Cases[1])
	}
	if run.Cases[0].Part != "train" && run.Cases[0].Part != "test" {
		t.Errorf("part %q", run.Cases[0].Part)
	}
	if len(run.Sessions) == 0 || run.Sessions[0].Session != "s1" || run.Sessions[0].Chars != 30 || len(run.Sessions[0].IDs) != 1 {
		t.Errorf("sessions %+v", run.Sessions)
	}
	if !strings.Contains(string(b), `"ids": []`) && strings.Contains(string(b), `"ids": null`) {
		t.Error("an empty id list is [] and not null")
	}
	// 비어 있는 비율은 null로 나온다(위임이 없는 부분이 있을 수 있다).
	if strings.Contains(string(b), "NaN") {
		t.Error("NaN")
	}
}

// tagWording stands in for a message catalog: every line is <method args>, so a test sees which
// text went where and the domain cannot slip in words of its own.
type tagWording struct{}

func tag(name string, args ...any) string { return fmt.Sprintf("<%s %v>", name, args) }

func (tagWording) Part(p string) string                            { return "<part " + p + ">" }
func (tagWording) Header(a, n, s string) string                    { return tag("header", a, n, s) }
func (tagWording) Units(c, u, tr, te int) string                   { return tag("units", c, u, tr, te) }
func (tagWording) TestSize(b bench.Bundles, p bench.Points) string { return tag("testSize", b, p) }
func (tagWording) Requests(n int) string                           { return tag("requests", n) }
func (tagWording) Hit(r string, n int) string                      { return tag("hit", r, n) }
func (tagWording) Wrong(r string, i, c, n, w, f, fc int) string {
	return tag("wrong", r, i, c, n, w, f, fc)
}
func (tagWording) Recover(r, m string) string   { return tag("recover", r, m) }
func (tagWording) Empty(r string) string        { return tag("empty", r) }
func (tagWording) CharsRequest(d string) string { return tag("charsRequest", d) }
func (tagWording) CharsTool(d string) string    { return tag("charsTool", d) }
func (tagWording) CharsSession(d string) string { return tag("charsSession", d) }
func (tagWording) CharsStart(d string) string   { return tag("charsStart", d) }
func (tagWording) Subagent(c int, h string, bl int, w string, f int, e, ch, lc string) string {
	return tag("subagent", c, h, bl, w, f, e, ch, lc)
}
func (tagWording) Duplicate(c int, h, o string, f int) string { return tag("duplicate", c, h, o, f) }
func (tagWording) ZeroDen() string                            { return "<zeroDen>" }
func (tagWording) Dist(m, md float64, mx, n int) string       { return tag("dist", m, md, mx, n) }

func TestReport_Text는_판정용_크기와_한_사례당_퍼센트포인트를_밝힌다(t *testing.T) {
	r, _ := runFake(t, reportCases, fakeScript(), keyed{sim: map[string][]string{"t5": {"d"}}})
	text := r.Text(bench.PartAll, tagWording{})
	for _, want := range []string{"== fake ==", "<testSize ", "<units [5 ", "100.0%", "<dist "} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Hangul, r) }) {
		t.Errorf("the domain wrote words of its own:\n%s", text)
	}
}

func TestReport_Text는_고른_부분의_지표_묶음만_보인다(t *testing.T) {
	r, _ := runFake(t, reportCases, fakeScript(), keyed{sim: map[string][]string{"t5": {"d"}}})
	blocks := map[string]string{bench.PartTrain: "[<part train>]", bench.PartTest: "[<part test>]", bench.PartAll: "[<part all>]"}
	for part, want := range map[string][]string{
		bench.PartTrain: {bench.PartTrain},
		bench.PartTest:  {bench.PartTest},
		bench.PartAll:   {bench.PartTrain, bench.PartTest, bench.PartAll},
	} {
		text := r.Text(part, tagWording{})
		for p, label := range blocks {
			if strings.Contains(text, label) != slices.Contains(want, p) {
				t.Errorf("part %s: %s shown = %v\n%s", part, label, strings.Contains(text, label), text)
			}
		}
		if !strings.Contains(text, "<testSize ") {
			t.Errorf("part %s: the size of the test part is always there", part)
		}
	}
}

func TestReport_Text는_분모_0을_밝힌다(t *testing.T) {
	// c3 is the only request of s2 and expects nothing: when nothing is injected the wrong recall has no denominator.
	r, _ := runFake(t, `{"id":"c3","session":"s2","prompt":"p3"}`, script{}, keyed{})
	if text := r.Text(bench.PartAll, tagWording{}); !strings.Contains(text, "<zeroDen>") {
		t.Errorf("no zero denominator mark:\n%s", text)
	}
}
