package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 저장 때 정리(설계 6.1, ORBIT-25 G10, 계획 D8·D9·M7): memory_save는 비슷한 기억이 있으면 저장하지 않고 고르기를
// 돌려주고, resolve로 고른 대로 적용하며, 같은 idempotencyKey로 다시 와도 한 번만 적용한다.

const appScope = "project:github.com/me/app"

func deployArgs(title, body string) map[string]any {
	return map[string]any{"kind": "lesson", "appliesTo": "user", "title": title, "body": body, "source": map[string]any{"kind": "session", "ref": "t:s1"}}
}

var (
	deployBase   = [2]string{"배포 스크립트는 드라이런을 먼저 돌린다", "운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다"}
	deployReword = [2]string{"배포 스크립트는 드라이런이 먼저다", "운영 배포 스크립트는 처음 돌릴 때 드라이런으로 바뀌는 것을 먼저 본 뒤에 실제로 돌린다"}
)

// saveMCP saves through the MCP tool and fails the test on a tool error.
func saveMCP(t *testing.T, cs *mcp.ClientSession, args map[string]any) SaveOut {
	t.Helper()
	var out SaveOut
	if res := call(t, cs, "memory_save", args, &out); res.IsError {
		t.Fatalf("memory_save %v: %s", args, text(res))
	}
	return out
}

// saveErr saves through the MCP tool and wants a tool error, whose text it returns.
func saveErr(t *testing.T, cs *mcp.ClientSession, args map[string]any) string {
	t.Helper()
	res := call(t, cs, "memory_save", args, nil)
	if !res.IsError {
		t.Fatalf("memory_save %v: no error: %s", args, text(res))
	}
	return text(res)
}

func with(args map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range args {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func live(t *testing.T, s *memory.Store) []memory.Memory {
	t.Helper()
	ms, err := s.List(memory.StateCandidate, memory.StateConfirmed)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

// 거의 같은 기억 넷은 서로의 낱말을 흔한 낱말로 만들어 점수를 깎는다(D4 IDF, 잰 값 0.56~0.58). 상위 3을 보려고
// 이 시험만 문턱을 Options로 0.4에 둔다. 기본 문턱의 고르기는 아래 시험들과 L-16 사례가 본다.
func TestSave_비슷한_기억이_있으면_저장하지_않고_상위_3과_고를_것을_돌려준다(t *testing.T) {
	cs, s := connectWith(t, Options{Project: appScope, ProjectLabel: "github.com/me/app", SaveSimilarMin: 0.4})
	long := strings.Repeat("운영 배포 스크립트를 처음 돌릴 때는 드라이런으로 바뀔 것을 먼저 보고 나서 실제로 돌린다. ", 6)
	var ids []string
	for i, title := range []string{deployBase[0], "배포 스크립트는 드라이런부터 돌린다", "배포 스크립트 드라이런을 먼저 한다", "배포 스크립트는 먼저 드라이런으로 본다"} {
		body := deployBase[1]
		if i == 0 {
			body = long
		}
		out := saveMCP(t, cs, with(deployArgs(title, body), "resolve", "add"))
		ids = append(ids, out.Memory.ID)
	}
	before := len(live(t, s))
	res := call(t, cs, "memory_save", deployArgs(deployReword[0], deployReword[1]), nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	var out SaveOut
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Saved || out.Memory.ID != "" || len(out.Similar) != memory.SimilarLimit {
		t.Fatalf("saved:false + 3 similar expected: %s", raw)
	}
	for _, field := range []string{`"saved":false`, `"similar":`, `"choices":`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("%s 없음: %s", field, raw)
		}
	}
	for _, field := range []string{`"memory"`, `"score"`, `"kind"`} { // 점수는 내부 값이다(M5), 기억 칸은 저장했을 때만
		if strings.Contains(string(raw), field) {
			t.Errorf("%s가 있다: %s", field, raw)
		}
	}
	want := []string{"add"}
	for _, m := range out.Similar {
		if !slices.Contains(ids, m.ID) || m.Title == "" || m.Summary == "" {
			t.Errorf("similar = %+v", m)
		}
		if utf8.RuneCountInString(m.Summary) > 200 || strings.Contains(m.Summary, "\n") {
			t.Errorf("summary %d룬: %q", utf8.RuneCountInString(m.Summary), m.Summary)
		}
		want = append(want, "update:"+m.ID)
	}
	for _, m := range out.Similar {
		want = append(want, "supersede:"+m.ID)
	}
	want = append(want, "skip")
	if !slices.Equal(out.Choices, want) {
		t.Errorf("choices = %v, want %v", out.Choices, want)
	}
	if n := len(live(t, s)); n != before {
		t.Errorf("saved:false가 기억을 썼다: %d → %d", before, n)
	}
	// 주제를 싣는다: 주제 있는 비슷한 기억(같은 주제 값이 아니도록 다른 주제로 저장)
	cs, _ = connect(t)
	topical := saveMCP(t, cs, with(deployArgs("배포 스크립트 드라이런 규칙", deployBase[1]), "kind", "decision", "topic", "배포 드라이런", "resolve", "add"))
	again := saveMCP(t, cs, with(deployArgs("배포 스크립트 드라이런 먼저", deployReword[1]), "kind", "decision", "topic", "배포 순서"))
	found := false
	for _, m := range again.Similar {
		if m.ID == topical.Memory.ID {
			found = m.Topic == "배포 드라이런"
		}
	}
	if again.Saved || !found {
		t.Errorf("주제 있는 비슷한 기억이 topic과 함께 오지 않았다: %+v", again)
	}
}

func TestSave_같은_제목과_같은_주제는_고르기_없이_저장한다(t *testing.T) {
	cs, s := connect(t)
	first := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	if !first.Saved || first.Memory.ID == "" {
		t.Fatalf("first = %+v", first)
	}
	same := saveMCP(t, cs, deployArgs(deployBase[0], deployReword[1]))
	if !same.Saved || same.Memory.ID != first.Memory.ID || len(same.Similar) != 0 {
		t.Fatalf("같은 제목 단축 = %+v", same)
	}
	dec := map[string]any{"kind": "decision", "title": "기억 문턱은 0.68이다", "body": "저장 때 비슷한 기억 문턱", "topic": "저장 문턱", "source": map[string]any{"kind": "session", "ref": "t:s1"}}
	old := saveMCP(t, cs, dec)
	nw := saveMCP(t, cs, with(dec, "title", "기억 문턱은 0.7이다", "body", "저장 때 비슷한 기억 문턱을 올린다"))
	if !nw.Saved || nw.Memory.ID == old.Memory.ID || !slices.Contains(nw.Superseded, old.Memory.ID) || len(nw.Choices) != 0 {
		t.Fatalf("같은 주제 대체 = %+v", nw)
	}
	if n := len(live(t, s)); n != 2 {
		t.Errorf("live = %d", n)
	}
}

func TestSave_resolve_add는_비슷해도_새로_저장한다(t *testing.T) {
	cs, s := connect(t)
	saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	out := saveMCP(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "add"))
	if !out.Saved || out.Memory.ID == "" || len(live(t, s)) != 2 {
		t.Fatalf("add = %+v, live %d", out, len(live(t, s)))
	}
}

func TestSave_resolve_update는_그_기억을_고치고_새_출처를_근거로_더한다(t *testing.T) {
	cs, s := connect(t)
	orig := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	link := saveMCP(t, cs, with(deployArgs("Docker로 DB를 띄운다", "시험 데이터베이스는 컨테이너다"), "kind", "fact"))
	args := with(deployArgs(deployReword[0], deployReword[1]), "resolve", "update:"+orig.Memory.ID, "tags", []string{"배포"},
		"source", map[string]any{"kind": "file", "ref": "/tmp/runbook.md"}, "links", []map[string]any{{"id": link.Memory.ID}})
	out := saveMCP(t, cs, args)
	if !out.Saved || out.Memory.ID != orig.Memory.ID || out.Memory.Title != deployReword[0] || out.Memory.Body != deployReword[1] ||
		out.Memory.Version != orig.Memory.Version+1 || !slices.Equal(out.Memory.Tags, []string{"배포"}) {
		t.Fatalf("update = %+v", out.Memory)
	}
	got, _ := s.Get(orig.Memory.ID)
	refs := []string{}
	for _, src := range got.Sources {
		refs = append(refs, src.Ref)
	}
	if !slices.Contains(refs, "/tmp/runbook.md") || !slices.Contains(refs, "t:s1") {
		t.Errorf("sources = %v", refs)
	}
	if len(got.Links) != 1 || got.Links[0].MemoryID != link.Memory.ID {
		t.Errorf("links = %+v", got.Links)
	}
	if len(live(t, s)) != 2 {
		t.Errorf("update가 기억을 더 만들었다")
	}
	if msg := saveErr(t, cs, with(args, "resolve", "update:m-none")); msg == "" {
		t.Error("없는 id")
	}
}

func TestSave_resolve_update는_사용자_말_기억을_사용자_말_없이_고치지_못한다(t *testing.T) {
	cs, s := connect(t)
	quote := saveMCP(t, cs, with(deployArgs(deployBase[0], deployBase[1]), "source", map[string]any{"kind": "quote", "excerpt": "배포는 드라이런 먼저"}))
	msg := saveErr(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "update:"+quote.Memory.ID))
	if !strings.Contains(msg, "userQuote") && !strings.Contains(msg, "사용자") {
		t.Errorf("guard message = %s", msg)
	}
	if got, _ := s.Get(quote.Memory.ID); got.Title != deployBase[0] || got.Version != quote.Memory.Version || len(got.Sources) != 1 {
		t.Errorf("바뀌었다: %+v", got)
	}
	// 새 출처가 사용자 말 그대로면 그 말이 근거다(D1).
	out := saveMCP(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "update:"+quote.Memory.ID,
		"source", map[string]any{"kind": "quote", "excerpt": "드라이런이 먼저라고 고쳐 줘"}))
	if out.Memory.Title != deployReword[0] {
		t.Errorf("quote update = %+v", out.Memory)
	}
}

func TestSave_resolve_supersede는_옛_판을_낡게_하고_새로_저장한다(t *testing.T) {
	cs, s := connect(t)
	orig := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	out := saveMCP(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "supersede:"+orig.Memory.ID))
	old, _ := s.Get(orig.Memory.ID)
	if !out.Saved || out.Memory.ID == orig.Memory.ID || old.State != memory.StateStale || old.SupersededBy != out.Memory.ID ||
		!slices.Contains(out.Superseded, orig.Memory.ID) || strings.Contains(strings.Join(out.Warnings, " "), "supersede:") {
		t.Fatalf("supersede = %+v, old %s→%s", out, old.State, old.SupersededBy)
	}
	// 이미 낡은 판을 고르면 새 판은 저장되지만 아무것도 대체하지 않았다고 알린다.
	again := saveMCP(t, cs, with(deployArgs("배포 스크립트 드라이런은 먼저", deployReword[1]), "resolve", "supersede:"+orig.Memory.ID))
	if !again.Saved || len(again.Superseded) != 0 || !strings.Contains(strings.Join(again.Warnings, " "), "supersede:"+orig.Memory.ID) {
		t.Errorf("stale target = %+v", again)
	}
}

func TestSave_믿을_수_없는_출처의_supersede는_거절한다(t *testing.T) {
	cs, s := connect(t)
	orig := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	msg := saveErr(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "supersede:"+orig.Memory.ID,
		"source", map[string]any{"kind": "file", "ref": "https://example.com/x", "trust": "untrusted"}))
	if msg == "" {
		t.Fatal("no message")
	}
	if got, _ := s.Get(orig.Memory.ID); got.State != memory.StateCandidate || len(live(t, s)) != 1 {
		t.Errorf("옛 판 %s, live %d", got.State, len(live(t, s)))
	}
}

func TestSave_resolve_skip은_아무것도_쓰지_않고_그_기억을_돌려준다(t *testing.T) {
	cs, s := connect(t)
	orig := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	for _, r := range []string{"skip", "skip:" + orig.Memory.ID} {
		out := saveMCP(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", r))
		if out.Saved || out.Resolve != "skip" || out.Memory.ID != orig.Memory.ID || out.Memory.Version != orig.Memory.Version {
			t.Fatalf("%s = %+v", r, out)
		}
	}
	if got, _ := s.Get(orig.Memory.ID); got.Version != orig.Memory.Version || len(got.Sources) != 1 || len(live(t, s)) != 1 {
		t.Errorf("skip이 썼다: %+v", got)
	}
}

func TestSave_모르는_resolve는_도구_오류다(t *testing.T) {
	cs, s := connect(t)
	orig := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	for _, r := range []string{"merge", "update:", "supersede", "add:" + orig.Memory.ID, "supersede:m-none", "skip:m-none"} {
		if msg := saveErr(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", r)); !strings.Contains(msg, "resolve") && !strings.Contains(msg, "m-none") {
			t.Errorf("%s: %s", r, msg)
		}
	}
	if len(live(t, s)) != 1 {
		t.Error("잘못된 resolve가 썼다")
	}
}

func TestSave_같은_키로_다시_부르면_한_번만_적용하고_링크를_다시_건다(t *testing.T) {
	cs, s := connect(t)
	other := saveMCP(t, cs, with(deployArgs("Docker로 DB를 띄운다", "시험 데이터베이스는 컨테이너다"), "kind", "fact"))
	args := with(deployArgs(deployBase[0], deployBase[1]), "idempotencyKey", "k-save", "links", []map[string]any{{"id": other.Memory.ID, "note": "함께"}})
	first := saveMCP(t, cs, args)
	if _, err := s.Unlink(first.Memory.ID, other.Memory.ID, memory.LinkOriginAI, "시험"); err != nil {
		t.Fatal(err)
	}
	second := saveMCP(t, cs, args)
	if !second.Saved || second.Memory.ID != first.Memory.ID || second.Memory.Version != first.Memory.Version {
		t.Fatalf("replay = %+v", second.Memory)
	}
	if len(second.Memory.Links) != 1 || second.Memory.Links[0].MemoryID != other.Memory.ID {
		t.Errorf("링크가 빠진 결과: %+v", second.Memory.Links)
	}
	if got, _ := s.Get(first.Memory.ID); len(got.Sources) != 1 || len(live(t, s)) != 2 {
		t.Errorf("다시 적용했다: sources %d, live %d", len(got.Sources), len(live(t, s)))
	}
	// resolve마다 한 번
	probe := deployArgs(deployReword[0], deployReword[1])
	if out := saveMCP(t, cs, with(probe, "idempotencyKey", "k-probe")); out.Saved {
		t.Fatalf("probe saved: %+v", out)
	}
	upd := with(probe, "idempotencyKey", "k-probe", "resolve", "update:"+first.Memory.ID) // saved:false는 기록하지 않아 같은 키로 고른다
	a := saveMCP(t, cs, upd)
	b := saveMCP(t, cs, upd)
	if a.Memory.Version != first.Memory.Version+1 || b.Memory.Version != a.Memory.Version || !b.Saved {
		t.Errorf("update twice = %d %d", a.Memory.Version, b.Memory.Version)
	}
	sup := with(probe, "idempotencyKey", "k-sup", "resolve", "supersede:"+first.Memory.ID)
	c := saveMCP(t, cs, sup)
	d := saveMCP(t, cs, sup)
	if c.Memory.ID != d.Memory.ID || !d.Saved || len(live(t, s)) != 2 {
		t.Errorf("supersede twice = %s %s live %d", c.Memory.ID, d.Memory.ID, len(live(t, s)))
	}
	skip := with(deployArgs("배포 스크립트 드라이런은 먼저", deployReword[1]), "idempotencyKey", "k-skip", "resolve", "skip")
	e := saveMCP(t, cs, skip)
	f := saveMCP(t, cs, skip)
	if e.Saved || f.Saved || e.Memory.ID != c.Memory.ID || f.Memory.ID != e.Memory.ID || f.Resolve != "skip" {
		t.Errorf("skip twice = %+v / %+v", e, f)
	}
	if r, ok, _ := s.SaveRequest("k-skip"); !ok || r.Action != memory.ApplySkip {
		t.Errorf("skip 기록 = %+v %v", r, ok)
	}
	if _, ok, _ := s.SaveRequest("k-probe-none"); ok {
		t.Error("없는 키")
	}
}

func TestSave_같은_키에_다른_인자는_충돌_오류다(t *testing.T) {
	cs, s := connect(t)
	saveMCP(t, cs, with(deployArgs(deployBase[0], deployBase[1]), "idempotencyKey", "k-1"))
	msg := saveErr(t, cs, with(deployArgs("커밋 메시지는 한글로 쓴다", "커밋 제목과 본문"), "idempotencyKey", "k-1"))
	if !strings.Contains(msg, "idempotencyKey") {
		t.Errorf("conflict = %s", msg)
	}
	if len(live(t, s)) != 1 {
		t.Error("충돌인데 썼다")
	}
	// 키·공백 순서만 다른 같은 인자는 같은 요청이다(정규화한 입력의 해시).
	if out := saveMCP(t, cs, map[string]any{"idempotencyKey": "k-1", "source": map[string]any{"ref": "t:s1", "kind": "session"}, "appliesTo": "user", "body": deployBase[1], "title": deployBase[0], "kind": "lesson"}); !out.Saved {
		t.Errorf("같은 인자 = %+v", out)
	}
}

func TestSave_MCP와_host_call_Tools_Call은_같은_값이다(t *testing.T) {
	cs, s := connect(t)
	tl := NewTools(s, Options{Project: appScope, ProjectLabel: "github.com/me/app", Actor: memory.ActorAI})
	other := saveMCP(t, cs, with(deployArgs("Docker로 DB를 띄운다", "시험 데이터베이스는 컨테이너다"), "kind", "fact"))
	saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	keyed := with(deployArgs(deployReword[0], deployReword[1]), "idempotencyKey", "k-same", "resolve", "add", "links", []map[string]any{{"id": other.Memory.ID}})
	saveMCP(t, cs, keyed) // 처음 적용. 아래 세 호출은 아무것도 쓰지 않는다(고르기, 같은 키, skip)
	probe := deployArgs("배포 스크립트 드라이런은 먼저", deployReword[1])
	for _, args := range []map[string]any{probe, keyed, with(probe, "resolve", "skip")} {
		in, _ := json.Marshal(args)
		got, err := tl.Call("memory_save", in, "claude", "s1")
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		raw, _ := json.Marshal(got)
		_ = json.Unmarshal(raw, &a)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "memory_save", Arguments: args})
		if err != nil || res.IsError {
			t.Fatal(err, text(res))
		}
		raw2, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw2, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%v: Tools.Call과 MCP가 다르다:\n%s\n%s", args["resolve"], raw, raw2)
		}
	}
}

func TestSave_문턱은_Options의_saveSimilarMin을_쓴다(t *testing.T) {
	cs, s := connectWith(t, Options{Project: appScope, ProjectLabel: "github.com/me/app", SaveSimilarMin: 0.99})
	saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	if out := saveMCP(t, cs, deployArgs(deployReword[0], deployReword[1])); !out.Saved || len(live(t, s)) != 2 {
		t.Errorf("문턱 0.99에서 고르기를 냈다: %+v", out)
	}
	cs2, s2 := connectWith(t, Options{Project: appScope, ProjectLabel: "github.com/me/app", LoadSaveSimilarMin: func() float64 { return 0.99 }})
	saveMCP(t, cs2, deployArgs(deployBase[0], deployBase[1]))
	if out := saveMCP(t, cs2, deployArgs(deployReword[0], deployReword[1])); !out.Saved || len(live(t, s2)) != 2 {
		t.Errorf("LoadSaveSimilarMin을 읽지 않았다: %+v", out)
	}
}

type dupMemory struct {
	How       string `json:"how"`
	KnownMiss string `json:"knownMiss"`
	Kind      string `json:"kind"`
	AppliesTo string `json:"appliesTo"`
	Topic     string `json:"topic"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

func (m dupMemory) args() map[string]any {
	a := map[string]any{"kind": m.Kind, "appliesTo": "user", "title": m.Title, "source": map[string]any{"kind": "session", "ref": "t:dup"}}
	for k, v := range map[string]string{"body": m.Body, "appliesTo": m.AppliesTo, "topic": m.Topic} {
		if v != "" {
			a[k] = v
		}
	}
	return a
}

// scoreOf is the find score of the memory id against m about to be saved (0 when it is not among the top).
func scoreOf(t *testing.T, s *memory.Store, m dupMemory, id string) float64 {
	t.Helper()
	r, err := s.FindScored(memory.FindInput{Query: strings.Join([]string{m.Title, m.Topic, m.Body}, " "), Scope: memory.ScopeGlobal, Limit: memory.SimilarLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range r {
		if h.Memory.ID == id {
			return h.Score
		}
	}
	return 0
}

// L-16(설계 6.1): 중복 사례 묶음을 다시 저장해도 같은 내용 기억이 둘 생기지 않는다. 원래 기억과 무관한 기억 여럿을
// 둔 DB에서 변형마다 저장하면 같은 제목 단축·같은 주제 대체로 바로 합쳐지거나, saved:false와 함께 원래 기억이
// similar에 온다. 그때 skip과 update:<원래 id>를 번갈아 고르면 현재값은 그 사례에 하나뿐이다.
func TestSave_L16_중복_사례를_다시_저장해도_같은_내용_기억이_둘_생기지_않는다(t *testing.T) {
	raw, err := os.ReadFile("testdata/save_duplicates.json")
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		Others []dupMemory `json:"others"`
		Cases  []struct {
			Name     string      `json:"name"`
			Original dupMemory   `json:"original"`
			Variants []dupMemory `json:"variants"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	variants, caught := 0, map[string]int{}
	for _, c := range set.Cases {
		cs, s := connect(t)
		var others []string
		for _, o := range set.Others {
			others = append(others, saveMCP(t, cs, with(o.args(), "resolve", "add")).Memory.ID)
		}
		orig := saveMCP(t, cs, c.Original.args())
		if !orig.Saved {
			t.Fatalf("%s: 원래 기억이 고르기를 받았다(무관한 기억과 비슷함): %+v", c.Name, orig.Similar)
		}
	variants:
		for i, v := range c.Variants {
			variants++
			t.Logf("%s · %s: 원래 기억과의 점수 %.3f", c.Name, v.How, scoreOf(t, s, v, orig.Memory.ID))
			if v.KnownMiss != "" { // 저장하면 중복이 생긴다: 읽기 판정으로 못 잡는 것이 그대로인지만 본다
				chk, err := s.SaveCheck(memory.SaveInput{Kind: v.Kind, Scope: memory.ScopeGlobal, Title: v.Title, Body: v.Body, Topic: v.Topic,
					Source: memory.Source{Kind: memory.SourceSession, Ref: "t:dup"}}, 0)
				if err != nil {
					t.Fatal(err)
				}
				if chk.Verdict != memory.CheckNew {
					t.Errorf("%s · %s: 이제 잡힌다(%s) — fixture의 knownMiss를 지우세요", c.Name, v.How, chk.Verdict)
				}
				caught["못 잡음(knownMiss)"]++
				continue
			}
			out := saveMCP(t, cs, v.args())
			cur := func() []string {
				var ids []string
				for _, m := range live(t, s) {
					if !slices.Contains(others, m.ID) {
						ids = append(ids, m.ID)
					}
				}
				return ids
			}
			switch {
			case out.Saved && len(cur()) == 1:
				caught["바로 합침"]++ // 같은 제목 단축이나 같은 주제 대체
				t.Logf("%s · %s: 바로 합침(%s)", c.Name, v.How, out.Memory.ID)
				continue
			case out.Saved:
				t.Errorf("%s · %s: 고르기 없이 새 기억이 생겼다 (%s)", c.Name, v.How, out.Memory.ID)
				caught["놓침"]++
				break variants
			}
			ids := cur()
			if len(ids) != 1 {
				t.Errorf("%s · %s: 현재값 %v", c.Name, v.How, ids)
				break
			}
			var hit bool
			for _, m := range out.Similar {
				hit = hit || m.ID == ids[0]
			}
			if !hit {
				t.Errorf("%s · %s: similar에 원래 기억이 없다: %+v", c.Name, v.How, out.Similar)
				continue
			}
			caught["고르기"]++
			resolve := "skip"
			if i%2 == 1 {
				resolve = "update:" + ids[0]
			}
			if r := saveMCP(t, cs, with(v.args(), "resolve", resolve)); r.Memory.ID != ids[0] {
				t.Errorf("%s · %s · %s = %+v", c.Name, v.How, resolve, r.Memory)
			}
			if after := cur(); len(after) != 1 {
				t.Errorf("%s · %s: %s 뒤 현재값 %v", c.Name, v.How, resolve, after)
			}
		}
		if n := len(live(t, s)); n != len(set.Others)+1 {
			t.Errorf("%s: 현재값 %d, want %d (무관한 기억은 그대로)", c.Name, n, len(set.Others)+1)
		}
	}
	t.Logf("사례 %d개, 변형 %d개: %v", len(set.Cases), variants, caught)
	if caught["못 잡음(knownMiss)"] > 2 {
		t.Errorf("못 잡는 변형이 늘었다: %v", caught)
	}
	if variants < 15 {
		t.Errorf("변형 %d개", variants)
	}
}

// 같은 키의 두 연결(같은 DB 파일을 따로 연 두 서버)이 동시에 부르면 한 번만 적용한다(M7): 키 확인과 기록은
// 기억 쓰기와 같은 트랜잭션이다. 같은 제목 단축이 겹침을 가리지 않게 출처 수와 판본까지 본다.
func TestSave_같은_키를_두_연결이_동시에_부르면_한_번만_적용한다(t *testing.T) {
	path := t.TempDir() + "/m.db"
	var sessions []*mcp.ClientSession
	var stores []*memory.Store
	for i := 0; i < 2; i++ {
		s, err := memory.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		stores = append(stores, s)
		sessions = append(sessions, connectServer(t, New(s, Options{Project: appScope, ProjectLabel: "github.com/me/app"})))
	}
	for round := 0; round < 8; round++ {
		args := with(deployArgs(fmt.Sprintf("동시 저장 %d번 기억", round), fmt.Sprintf("두 연결이 같은 키로 %d번째 저장을 부른다", round)),
			"idempotencyKey", fmt.Sprintf("k-race-%d", round), "resolve", "add")
		ids := make([]string, 2)
		errs := make([]string, 2)
		var wg sync.WaitGroup
		for i := range sessions {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				res, err := sessions[i].CallTool(context.Background(), &mcp.CallToolParams{Name: "memory_save", Arguments: args})
				if err != nil || res.IsError {
					errs[i] = fmt.Sprint(err, text(res))
					return
				}
				var out SaveOut
				raw, _ := json.Marshal(res.StructuredContent)
				_ = json.Unmarshal(raw, &out)
				ids[i] = out.Memory.ID
			}(i)
		}
		wg.Wait()
		if errs[0] != "" || errs[1] != "" || ids[0] == "" || ids[0] != ids[1] {
			t.Fatalf("round %d: ids %v errs %v", round, ids, errs)
		}
		m, err := stores[0].Get(ids[0])
		if err != nil || m.Version != 1 || len(m.Sources) != 1 {
			t.Fatalf("round %d: 두 번 적용했다: version %d, sources %d (%v)", round, m.Version, len(m.Sources), err)
		}
	}
	if n := len(live(t, stores[0])); n != 8 {
		t.Errorf("live = %d", n)
	}
}

// 같은 키의 재시도는 판정보다 먼저 기록을 본다: 적용한 뒤 그 기억의 제목이 바뀌어도(같은 제목 단축이 안 되어도)
// 고르기를 다시 받지 않고 기록한 기억을 지금 상태로 받는다.
func TestSave_같은_키의_재시도는_고르기를_다시_받지_않는다(t *testing.T) {
	cs, _ := connect(t)
	args := with(deployArgs(deployBase[0], deployBase[1]), "idempotencyKey", "k-retitle")
	first := saveMCP(t, cs, args)
	var upd MemoryOut
	if res := call(t, cs, "memory_update", map[string]any{"id": first.Memory.ID, "title": deployReword[0]}, &upd); res.IsError {
		t.Fatal(text(res))
	}
	again := saveMCP(t, cs, args)
	if !again.Saved || again.Memory.ID != first.Memory.ID || again.Memory.Title != deployReword[0] || len(again.Similar) != 0 {
		t.Fatalf("retry = %+v", again)
	}
}

// update에 pin을 주면 고친 뒤 승격한다. 후보라 올리지 못하면 고침은 그대로 두고 알린다(오류로 되돌리지 않는다).
func TestSave_resolve_update의_pin은_승격하지_못하면_알린다(t *testing.T) {
	cs, s := connect(t)
	orig := saveMCP(t, cs, deployArgs(deployBase[0], deployBase[1]))
	out := saveMCP(t, cs, with(deployArgs(deployReword[0], deployReword[1]), "resolve", "update:"+orig.Memory.ID, "pin", true))
	if !out.Saved || out.Memory.Title != deployReword[0] || out.Memory.Pinned || !strings.Contains(strings.Join(out.Warnings, " "), "update:"+orig.Memory.ID) {
		t.Fatalf("pin candidate = %+v", out)
	}
	if got, _ := s.Get(orig.Memory.ID); got.Pinned || got.Title != deployReword[0] {
		t.Errorf("got %+v", got)
	}
}
