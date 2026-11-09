//go:build benchequiv

// 같음 증거(계획 orbit-mem-1-5 a5): 얼린 사본이 87ab0fb의 inject를 훅과 같은 인자로 부른 것과 단계마다 같은 id·같은 글을
// 내는지 본다. 1-4(inject)·1-3(검색)이 합쳐지면 다르게 나오는 것이 정상이므로 기본 go test에는 들지 않는다:
//
//	go test -tags benchequiv ./internal/bench/legacy -count=1 -v
package legacy_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/legacy"
	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/place"
	"github.com/crispebble/orbit-mem/internal/project"
)

// reference is the live inject called the way the host of 87ab0fb called it for each hook
// (internal/host/ops.go:87-93 adopt, 149-158 locate, 188-194 shown, 240-290 context·recall·tool-context).
type reference struct {
	s   *memory.Store
	cfg config.Config
}

func newReference(t *testing.T, db string, set *bench.Set) *reference {
	t.Helper()
	cfg := config.Default()
	if len(set.Products) > 0 {
		if err := json.Unmarshal(set.Products, &cfg.Products); err != nil {
			t.Fatal(err)
		}
	}
	s, err := memory.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	at := set.At()
	s.Policy = memory.Policy{SeenDays: cfg.ConfirmSeenDays, FollowedVerified: cfg.ConfirmFollowedVerified, CorrectionSessions: cfg.CorrectionRepeatSessions}
	s.Now = func() time.Time { return at }
	return &reference{s: s, cfg: cfg}
}

func (r *reference) where(cwd string) string {
	var p project.Project
	if cwd != "" {
		p = project.Resolve(cwd)
	}
	place.Known(p)
	if p.Dir != "" {
		return p.Dir
	}
	return p.Scope
}

func (r *reference) shown(session string, inj inject.Injection) bench.Shown {
	inject.RecordShown(r.s, legacy.Host, session, inj)
	ids := inj.MemoryIDs
	if ids == nil {
		ids = []string{}
	}
	return bench.Shown{IDs: ids, Chars: inj.Chars, Text: inj.Text}
}

func (r *reference) Name() string { return "inject-87ab0fb" }

func (r *reference) Start(session, cwd string) (bench.Shown, error) {
	where := r.where(cwd)
	return r.shown(session, inject.SessionBundleWith(r.s, r.cfg, where, legacy.Host, session, inject.Options{})), nil
}

func (r *reference) Request(session, cwd, prompt string) (bench.Shown, error) {
	where := r.where(cwd)
	return r.shown(session, inject.PromptInjectionFor(r.s, r.cfg, where, prompt, legacy.Host, session, inject.Options{})), nil
}

func (r *reference) Tool(session, cwd, tool string) (bench.Shown, error) {
	where := r.where(cwd)
	return r.shown(session, inject.ToolInjection(r.s, r.cfg, where, legacy.Host, session, tool)), nil
}

func (r *reference) Subagent(session, cwd, task string) (bench.Shown, error) {
	r.where(cwd)
	return r.shown(session, inject.SubagentStart(r.s, r.cfg)), nil
}

// step is one hook call, or a change made to both DBs between calls (a memory confirmed or
// replaced elsewhere while a session runs).
type step struct {
	op, session, cwd, arg string
	change                func(t *testing.T, s *memory.Store, id func(string) string)
}

// tally counts the compared hook calls: same is exactly the same, tied the same once the members of a
// field pack with the same UpdatedAt are put in id order (87ab0fb left them in map order, inject.go:648).
type tally struct{ steps, same, tied, nonEmpty int }

var memberLine = regexp.MustCompile(`^- .* \((m-[^ )]+)\)$`)

// canonical puts the members of each field pack signal block that share an UpdatedAt in id order, in the
// text and in the ids, the order the frozen copy keeps. Other lines and ids stay where they are.
func canonical(t *testing.T, s *memory.Store, in bench.Shown) bench.Shown {
	t.Helper()
	member := map[string]memory.Memory{}
	for _, id := range in.IDs {
		if m, err := s.Get(id); err == nil && memory.DomainOf(m) != "" {
			member[id] = m
		}
	}
	less := func(a, b string) bool {
		if member[a].UpdatedAt != member[b].UpdatedAt {
			return member[a].UpdatedAt > member[b].UpdatedAt
		}
		return a < b
	}
	lines := strings.Split(in.Text, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "[분야 「") {
			continue
		}
		j := i + 1
		for j < len(lines) && memberLine.MatchString(lines[j]) {
			j++
		}
		run := lines[i+1 : j]
		idOf := func(l string) string { return memberLine.FindStringSubmatch(l)[1] }
		sort.SliceStable(run, func(a, b int) bool { return less(idOf(run[a]), idOf(run[b])) })
		i = j - 1
	}
	out := bench.Shown{Chars: in.Chars, Text: strings.Join(lines, "\n"), IDs: slices.Clone(in.IDs)}
	for i := 0; i < len(out.IDs); {
		if _, ok := member[out.IDs[i]]; !ok {
			i++
			continue
		}
		j := i
		for j < len(out.IDs) && member[out.IDs[j]].ID != "" {
			j++
		}
		sort.SliceStable(out.IDs[i:j], func(a, b int) bool { return less(out.IDs[i+a], out.IDs[i+b]) })
		i = j
	}
	return out
}

func equal(a, b bench.Shown) bool {
	return slices.Equal(a.IDs, b.IDs) && a.Text == b.Text && a.Chars == b.Chars
}

// compare runs the steps on two fresh copies, legacy on one and the reference on the other, and
// fails on the first step whose ids, text or length differ.
func compare(t *testing.T, p *bench.Prepared, steps []step) (tally, []string) {
	t.Helper()
	dbL, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	dbR, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	l, closeL, err := legacy.New(dbL, p.Set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeL() })
	ref := newReference(t, dbR, p.Set)
	var n tally
	var texts []string
	for i, st := range steps {
		if st.change != nil {
			for _, db := range []string{dbL, dbR} {
				s, err := memory.Open(db)
				if err != nil {
					t.Fatal(err)
				}
				later := p.Set.At().Add(time.Hour)
				s.Now = func() time.Time { return later }
				st.change(t, s, p.IDOf)
				s.Close()
			}
			continue
		}
		call := func(r bench.Recaller) bench.Shown {
			var s bench.Shown
			var err error
			switch st.op {
			case "start":
				s, err = r.Start(st.session, st.cwd)
			case "request":
				s, err = r.Request(st.session, st.cwd, st.arg)
			case "tool":
				s, err = r.Tool(st.session, st.cwd, st.arg)
			case "subagent":
				s, err = r.Subagent(st.session, st.cwd, st.arg)
			default:
				t.Fatalf("step %d: op %q", i, st.op)
			}
			if err != nil {
				t.Fatalf("step %d %s: %v", i, r.Name(), err)
			}
			return s
		}
		got, want := call(l), call(ref)
		n.steps++
		where := fmt.Sprintf("step %d (%s %s %s %q)", i, st.op, st.session, st.cwd, st.arg)
		if !equal(got, canonical(t, ref.s, got)) {
			t.Fatalf("%s: legacy did not keep tied field pack members in id order\n%s", where, got.Text)
		}
		switch {
		case equal(got, want):
			n.same++
		case equal(got, canonical(t, ref.s, want)):
			n.tied++
			t.Logf("%s: the same once tied field pack members are in id order", where)
		default:
			t.Fatalf("%s differs\nlegacy %v (%d)\n%s\n--- inject %v (%d)\n%s", where, got.IDs, got.Chars, got.Text, want.IDs, want.Chars, want.Text)
		}
		if len(got.IDs) > 0 {
			n.nonEmpty++
		}
		texts = append(texts, got.Text)
	}
	return n, texts
}

// casesSteps are the hook calls bench.Run makes for a set: a session starts before its first case,
// then the request, the tool call or the sub agent of each case in file order.
func casesSteps(set *bench.Set) []step {
	var out []step
	started := map[string]bool{}
	for _, c := range set.Cases {
		if c.CaseKind() == bench.KindDuplicate {
			continue
		}
		key, cwd := c.SessionKey(), set.CWDOf(c)
		if !started[key] {
			started[key] = true
			out = append(out, step{op: "start", session: key, cwd: cwd})
		}
		switch c.CaseKind() {
		case bench.KindRequest:
			if c.Prompt != "" {
				out = append(out, step{op: "request", session: key, cwd: cwd, arg: c.Prompt})
			}
			if c.Tool != "" {
				out = append(out, step{op: "tool", session: key, cwd: cwd, arg: c.Tool})
			}
		case bench.KindSubagent:
			out = append(out, step{op: "subagent", session: key, cwd: cwd, arg: c.Task})
		}
	}
	return out
}

func TestEquiv_공개_묶음에서_사본과_87ab0fb_inject가_단계마다_같다(t *testing.T) {
	set, err := bench.Read("../testdata/public_set.json")
	if err != nil {
		t.Fatal(err)
	}
	// 불러오기는 휘발 기억의 날을 1~7로 받는다. 그 밖의 값은 7로 맞춰 견주고 기록에 남긴다(묶음 자료를 고치면 빈다).
	for i, m := range set.Memories {
		if m.TTLDays > 7 {
			t.Logf("public set: memory %q ttlDays %d → 7 for this comparison", m.Key, m.TTLDays)
			set.Memories[i].TTLDays = 7
		}
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	steps := casesSteps(p.Set)
	n, _ := compare(t, p, steps)
	t.Logf("public set: cases %d, hook calls %d, same %d, same up to tied order %d, non-empty %d", len(p.Set.Cases), n.steps, n.same, n.tied, n.nonEmpty)
	if n.steps == 0 || n.nonEmpty == 0 || n.same+n.tied != n.steps {
		t.Fatal("nothing compared")
	}
}

// richSet exercises what the public set does not reach: product core and a multi-product folder,
// the cut of recent decisions, the automatic-revert line, a correction and a new memory in the middle
// of a session, a review-due and an expired memory, an older topic value, a long rule body, a field pack
// cut by the budget, candidates after confirmed ones, tool prefixes and the tool limit.
const richSet = `{
  "now": "2026-11-03T09:00:00+09:00",
  "cwd": "/bench/app",
  "seed": "legacy-rich",
  "products": {
    "shop": {"match": ["/bench/app", "/bench/multi"], "names": ["샵"]},
    "desk": {"match": ["/bench/multi", "/bench/desk"], "names": ["데스크"]}
  },
  "memories": [
    {"key":"u-core1","kind":"rule","scope":"global","quote":true,"pinned":true,"title":"커밋 메시지는 한글로 쓴다","body":"제목은 50자 안에서 쓴다.\n본문은 왜를 쓴다.","topic":"커밋 메시지 언어","ageDays":20},
    {"key":"u-core2","kind":"lesson","scope":"global","quote":true,"pinned":true,"title":"시험이 실패하면 원인부터 찾는다","ageDays":12},
    {"key":"p-core","kind":"decision","scope":"product:shop","quote":true,"pinned":true,"title":"샵 결제는 카드만 받는다","topic":"샵 결제 수단","ageDays":10},
    {"key":"q-core","kind":"decision","scope":"product:desk","quote":true,"pinned":true,"title":"데스크 화면은 어두운 테마가 기본이다","topic":"데스크 테마","ageDays":9},
    {"key":"r-app","kind":"rule","scope":"here","quote":true,"title":"이 앱의 배포 스크립트는 make release로 돌린다","body":"BODY600","topic":"앱 배포 스크립트","ageDays":40},
    {"key":"r-app2","kind":"rule","scope":"here","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다.","topic":"앱 패키지 설치","ageDays":41},
    {"key":"d1","kind":"decision","scope":"global","quote":true,"title":"기록 형식은 JSON으로 정한다","topic":"기록 형식","ageDays":1},
    {"key":"d2","kind":"decision","scope":"global","quote":true,"title":"로그는 30일 보관한다","topic":"로그 보관","ageDays":2},
    {"key":"d3","kind":"decision","scope":"global","quote":true,"title":"문서는 저장소 안 docs 폴더에 둔다","topic":"문서 위치","ageDays":3},
    {"key":"d4","kind":"decision","scope":"global","quote":true,"title":"새 서비스는 Go로 쓴다","topic":"서비스 언어","ageDays":4},
    {"key":"d5","kind":"decision","scope":"global","quote":true,"title":"시각은 UTC로 저장한다","topic":"시각 저장","ageDays":5},
    {"key":"d6","kind":"decision","scope":"global","quote":true,"title":"설정 파일은 YAML을 쓰지 않는다","topic":"설정 파일 형식","ageDays":6},
    {"key":"d-repl","kind":"decision","scope":"global","title":"기록 형식은 JSON Lines로 바꾼다","ageDays":0},
    {"key":"cand-1","kind":"lesson","scope":"global","title":"운영 배포 전에는 백업 사본을 확인한다","ageDays":2},
    {"key":"conf-1","kind":"fact","scope":"global","quote":true,"title":"운영 배포 백업 사본은 매일 새벽에 만든다","ageDays":30},
    {"key":"review","kind":"fact","scope":"global","quote":true,"retention":"durable","reviewAfterDays":10,"title":"인증 토큰은 열두 시간 뒤 끝난다","ageDays":30},
    {"key":"expired","kind":"fact","scope":"global","retention":"volatile","ttlDays":3,"title":"인증 서버는 이번 주 점검 중이다","ageDays":5},
    {"key":"topic-old","kind":"fact","scope":"global","quote":true,"title":"캐시 서버는 6379 포트를 쓴다","topic":"캐시 서버 포트","ageDays":90},
    {"key":"topic-new","kind":"fact","scope":"global","quote":true,"title":"캐시 서버는 6380 포트를 쓴다","topic":"캐시 서버 포트","ageDays":8},
    {"key":"cand-new","kind":"rule","scope":"global","title":"릴리스 노트는 사용자 말로 쓴다","body":"내부 용어를 풀어 쓴다.","ageDays":1},
    {"key":"t1","kind":"rule","scope":"global","quote":true,"title":"셸에서 rm -rf는 경로를 두 번 본다","tools":["Bash"],"ageDays":3},
    {"key":"t2","kind":"lesson","scope":"global","title":"셸 명령은 작업 폴더를 매번 준다","tools":["Bash"],"ageDays":2},
    {"key":"t3","kind":"fact","scope":"here","quote":true,"title":"이 앱의 셸 스크립트는 bin 폴더에 있다","tools":["Ba*"],"ageDays":4},
    {"key":"t4","kind":"rule","scope":"global","quote":true,"title":"셸에서 비밀값을 출력하지 않는다","tools":["Bash"],"ageDays":6},
    {"key":"t5","kind":"fact","scope":"project:/bench/other","quote":true,"title":"다른 저장소의 셸은 zsh다","tools":["Bash"],"ageDays":1},
    {"key":"t6","kind":"journal","scope":"global","quote":true,"title":"셸 일지","tools":["Bash"],"ageDays":1},
    {"key":"t7","kind":"rule","scope":"global","quote":true,"title":"문서 도구로 쓰기 전에 공간을 확인한다","body":"공간 첫 화면 소개를 먼저 읽는다.","tools":["mcp__docs__*"],"ageDays":2},
    {"key":"s01","kind":"fact","scope":"global","title":"보안 점검 항목 하나: 비밀번호는 해시로만 저장한다","tags":["분야:보안"],"ageDays":11},
    {"key":"s02","kind":"fact","scope":"global","title":"보안 점검 항목 둘: 세션 쿠키에는 보안 속성을 단다","tags":["분야:보안"],"ageDays":12},
    {"key":"s03","kind":"fact","scope":"global","title":"보안 점검 항목 셋: 업로드 파일은 형식을 검사한다","tags":["분야:보안"],"ageDays":13},
    {"key":"s04","kind":"fact","scope":"global","title":"보안 점검 항목 넷: 관리자 화면은 사내망에서만 연다","tags":["분야:보안"],"ageDays":14},
    {"key":"s05","kind":"fact","scope":"global","title":"보안 점검 항목 다섯: 외부 입력은 질의에 바로 넣지 않는다","tags":["분야:보안"],"ageDays":15},
    {"key":"s06","kind":"fact","scope":"global","title":"보안 점검 항목 여섯: 오류 화면에 내부 경로를 보이지 않는다","tags":["분야:보안"],"ageDays":16},
    {"key":"s07","kind":"fact","scope":"global","title":"보안 점검 항목 일곱: 비밀값은 저장소에 넣지 않는다","tags":["분야:보안"],"ageDays":17},
    {"key":"s08","kind":"fact","scope":"global","title":"보안 점검 항목 여덟: 의존성 경고는 주마다 본다","tags":["분야:보안"],"ageDays":18},
    {"key":"s09","kind":"fact","scope":"global","title":"보안 점검 항목 아홉: 로그에 개인 정보를 남기지 않는다","tags":["분야:보안"],"ageDays":19},
    {"key":"s10","kind":"fact","scope":"global","title":"보안 점검 항목 열: 토큰은 짧게 살고 다시 받는다","tags":["분야:보안"],"ageDays":20},
    {"key":"s11","kind":"fact","scope":"global","title":"보안 점검 항목 열하나: 관리자 계정은 두 단계 인증을 쓴다","tags":["분야:보안"],"ageDays":21},
    {"key":"s12","kind":"fact","scope":"global","title":"보안 점검 항목 열둘: 백업은 암호화해 둔다","tags":["분야:보안"],"ageDays":22},
    {"key":"s13","kind":"fact","scope":"global","title":"보안 점검 항목 열셋: 권한은 가장 작게 준다","tags":["분야:보안"],"ageDays":23},
    {"key":"s14","kind":"fact","scope":"global","title":"보안 점검 항목 열넷: 비밀번호 재설정 링크는 한 번만 쓴다","tags":["분야:보안"],"ageDays":24},
    {"key":"s15","kind":"fact","scope":"global","title":"보안 점검 항목 열다섯: 외부 요청에는 시간 한도를 둔다","tags":["분야:보안"],"ageDays":25},
    {"key":"s16","kind":"fact","scope":"global","title":"보안 점검 항목 열여섯: 파일 경로는 정규화한 뒤 검사한다","tags":["분야:보안"],"ageDays":26},
    {"key":"s17","kind":"fact","scope":"global","title":"보안 점검 항목 열일곱: 응답 머리글에 보안 정책을 단다","tags":["분야:보안"],"ageDays":27},
    {"key":"s18","kind":"fact","scope":"global","title":"보안 점검 항목 열여덟: 계정 잠금은 다섯 번 실패 뒤다","tags":["분야:보안"],"ageDays":28},
    {"key":"s19","kind":"fact","scope":"global","title":"보안 점검 항목 열아홉: 인증서 만료를 미리 알린다","tags":["분야:보안"],"ageDays":29},
    {"key":"s20","kind":"fact","scope":"global","title":"보안 점검 항목 스물: 감사 기록은 지우지 않는다","tags":["분야:보안"],"ageDays":31},
    {"key":"s21","kind":"fact","scope":"global","title":"보안 점검 항목 스물하나: 공개 키는 돌려 쓴다","tags":["분야:보안"],"ageDays":32},
    {"key":"s22","kind":"fact","scope":"global","title":"보안 점검 항목 스물둘: 시험 계정은 운영에 두지 않는다","tags":["분야:보안"],"ageDays":33}
  ],
  "cases": [{"id":"c1","prompt":"아무 요청"}]
}`

func TestEquiv_인라인_묶음에서_사본과_87ab0fb_inject가_단계마다_같다(t *testing.T) {
	src := strings.Replace(richSet, "BODY600", strings.Repeat("배포 스크립트는 저장소 루트에서 돌린다. ", 30), 1)
	set, err := bench.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	steps := []step{
		// 세션 시작 전에 해로움 판정 되돌림이 하나 있었다.
		{change: func(t *testing.T, s *memory.Store, _ func(string) string) {
			if err := s.RecordEvent(memory.Event{Kind: memory.EventAutoReverted, At: p.Set.At().Add(-time.Hour).UnixMilli()}, map[string]any{"memoryId": "x"}); err != nil {
				t.Fatal(err)
			}
		}},
		{op: "start", session: "s1", cwd: "/bench/app"},
		{op: "start", session: "s1", cwd: "/bench/app"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "이 앱 배포 스크립트 make release 돌리는 법"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "운영 배포 전에 백업 사본 확인"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "보안 점검 항목 정리해줘"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "인증 토큰 끝나는 시간과 인증 서버 점검"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "캐시 서버 포트 몇 번 쓰지"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "커밋 메시지 한글로 쓰는 규칙"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "   "},
		{op: "tool", session: "s1", cwd: "/bench/app", arg: "Bash"},
		{op: "tool", session: "s1", cwd: "/bench/app", arg: "Bash"},
		{op: "tool", session: "s1", cwd: "/bench/app", arg: "mcp__docs__write_page"},
		{op: "tool", session: "s1", cwd: "/bench/app", arg: ""},
		// 세션 중에 다른 곳에서 후보가 확정되고 최근 결정 하나가 대체되었다.
		{change: func(t *testing.T, s *memory.Store, id func(string) string) {
			if _, err := s.SetState(id("cand-new"), memory.StateConfirmed, "bench"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Supersede(id("d1"), id("d-repl"), "bench"); err != nil {
				t.Fatal(err)
			}
		}},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "아무 관련 없는 질문 하나"},
		{op: "request", session: "s1", cwd: "/bench/app", arg: "아무 관련 없는 질문 하나"},
		{op: "subagent", session: "s1", cwd: "/bench/app", arg: "운영 배포 전에 백업 사본 확인하는 순서를 정리해"},
		{op: "start", session: "s2", cwd: "/bench/multi"},
		{op: "request", session: "s2", cwd: "/bench/multi", arg: "샵 결제 카드만 받는지 확인"},
		{op: "request", session: "s2", cwd: "/bench/multi", arg: "데스크 화면 테마 기본값"},
		{op: "start", session: "s3", cwd: "/bench/app/web"},
		{op: "request", session: "s3", cwd: "/bench/app/web", arg: "pnpm으로 앱 설치하는 방법"},
		{op: "tool", session: "s3", cwd: "/bench/app/web", arg: "Bash"},
		{op: "start", session: "s4", cwd: "/bench/other"},
		{op: "tool", session: "s4", cwd: "/bench/other", arg: "Bash"},
		{op: "request", session: "s4", cwd: "/bench/other", arg: "이 앱 배포 스크립트 make release 돌리는 법"},
		// 시작 없이 온 요청: 묶음을 지금 세션 시작이 넣을 것으로 본다.
		{op: "request", session: "s5", cwd: "/bench/app", arg: "커밋 메시지 한글로 쓰는 규칙"},
		{op: "start", session: "", cwd: ""},
		{op: "request", session: "", cwd: "", arg: "운영 배포 전에 백업 사본 확인"},
	}
	n, texts := compare(t, p, steps)
	t.Logf("inline set: hook calls %d, same %d, same up to tied order %d, non-empty %d", n.steps, n.same, n.tied, n.nonEmpty)
	// 묶음이 노리는 길을 실제로 지났는지 본다(지나지 않으면 같음이 증거가 되지 못한다).
	all := strings.Join(texts, "\n")
	for _, mark := range []string{"상시 핵심]", "shop 핵심]", "[최근 결정]", "해로움 판정", "먼저 찾는 곳]", "(전문은 memory_get)", "후보)", "재검토일 지남",
		"신호] 필요한", "개만 보임", "호출 직전]", "정정: ", "새 기억: "} {
		if !strings.Contains(all, mark) {
			t.Errorf("the inline set never reached %q", mark)
		}
	}
	for _, key := range []string{"expired", "topic-old", "t6"} {
		if strings.Contains(all, p.IDOf(key)) {
			t.Errorf("%s (expired, an older topic value or a journal) was shown", key)
		}
	}
}
