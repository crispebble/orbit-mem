package hostpath_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/crispebble/orbit-mem/internal/bench"
	"github.com/crispebble/orbit-mem/internal/bench/hostpath"
	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/memory"
	"github.com/crispebble/orbit-mem/internal/project"
)

var testHome string

func TestMain(m *testing.M) {
	// 시험이 이 PC의 실제 홈과 기억 DB를 열지 못하게 임시 홈을 건다.
	tmp, err := os.MkdirTemp("", "orbit-mem-hostpath-test-")
	if err != nil {
		panic(err)
	}
	testHome = tmp
	os.Setenv("ORBIT_HOME", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

const setJSON = `{"about":"t","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",
"memories":[
 {"key":"pnpm","kind":"rule","scope":"project:/bench/app","quote":true,"title":"이 앱은 pnpm으로 설치한다","body":"npm install을 쓰지 않는다. lock 파일은 pnpm-lock.yaml 하나만 둔다.","topic":"앱 패키지 설치","ageDays":40},
 {"key":"deploy","kind":"fact","scope":"global","quote":true,"title":"운영 배포는 main 병합으로 일어난다","body":"감시기가 origin/main을 보고 배포한다.","ageDays":3},
 {"key":"other","kind":"fact","scope":"global","title":"커피 취향은 오트 라떼","ageDays":2}
],
"cases":[{"id":"c1","origin":"user","kind":"request","prompt":"pnpm으로 설치해 줘","expect":["pnpm"]}]}`

type fixture struct {
	p   *bench.Prepared
	env bench.Env
	db  string
}

func open(t *testing.T) fixture { return openWith(t, nil) }

func openWith(t *testing.T, overrides map[string]string) fixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	set, err := bench.Parse([]byte(setJSON))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	db, err := p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	env, err := hostpath.FactoryWith(hostpath.Options{Set: overrides})(db, set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return fixture{p, env, db}
}

func TestRequest가_기대한_기억을_보이고_글자수를_룬으로_센다(t *testing.T) {
	f := open(t)
	got, err := f.env.Recaller.Request("s1", "/bench/app", "pnpm으로 설치해 줘")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.IDs, f.p.IDOf("pnpm")) {
		t.Fatalf("ids = %v, want %s in it", got.IDs, f.p.IDOf("pnpm"))
	}
	if got.Text == "" || got.Chars != utf8.RuneCountInString(got.Text) {
		t.Fatalf("chars = %d, text runes = %d", got.Chars, utf8.RuneCountInString(got.Text))
	}
}

func TestStart와_Subagent와_Tool은_오류없이_Shown을_낸다(t *testing.T) {
	f := open(t)
	start, err := f.env.Recaller.Start("s2", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if start.Chars != utf8.RuneCountInString(start.Text) {
		t.Fatalf("start chars = %d", start.Chars)
	}
	if _, err := f.env.Recaller.Subagent("s2", "/bench/app", "pnpm 설치 확인"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Recaller.Tool("s2", "/bench/app", "Bash", "pnpm install"); err != nil {
		t.Fatal(err)
	}
}

// 같은 세션의 재개 시작은 묶음 안 기억이 그 사이 낡았으면 묶음을 다시 만든다(명세 F-1, inject.SessionBundleWith). 0.19.0은
// 얼린 묶음을 그대로 다시 줘서 그 기억이 그 호출의 Dead로 나왔다. 이제 재개·새 세션·압축 뒤 시작 모두 Dead가 없다.
func TestHostpath_재개_시작은_낡은_기억을_뺀_묶음을_내고_Dead가_없다(t *testing.T) {
	f := open(t)
	// 세션 묶음(recall auto)에는 상시 핵심만 든다: 사용자 범위의 확정 기억 하나를 올려 둔다.
	core := f.p.IDOf("deploy")
	st, err := memory.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pin(core, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	first, err := f.env.Recaller.Start("s1", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(first.IDs, core) || len(first.Dead) != 0 {
		t.Fatalf("first start: ids %v dead %v, want core in and nothing dead", first.IDs, first.Dead)
	}
	s, err := memory.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(core, memory.StateStale, "낡음"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	again, err := f.env.Recaller.Start("s1", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(again.IDs, core) || strings.Contains(again.Text, core) || len(again.Dead) != 0 || len(again.DeadWhy) != 0 {
		t.Fatalf("resumed start: ids %v dead %v why %v, want the bundle rebuilt without core", again.IDs, again.Dead, again.DeadWhy)
	}
	fresh, err := f.env.Recaller.Start("s2", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fresh.IDs, core) || len(fresh.Dead) != 0 {
		t.Fatalf("new session: ids %v dead %v", fresh.IDs, fresh.Dead)
	}
	// 압축 뒤 시작(Reset 다음 Start)은 묶음을 새로 만든다: 낡은 기억이 빠진다.
	r, ok := f.env.Recaller.(bench.Resetter)
	if !ok {
		t.Fatal("the host path resets a session as the compact and clear starts do")
	}
	if err := r.Reset("s1"); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := f.env.Recaller.Start("s1", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(rebuilt.IDs, core) || len(rebuilt.Dead) != 0 {
		t.Fatalf("start after reset: ids %v dead %v", rebuilt.IDs, rebuilt.Dead)
	}
}

// 묶음에 든 기억을 지우면, 재개 시작은 그 기억을 뺀 묶음을 다시 만든다. 0.19.0은 사본에 없는 그 id를 까닭 gone의 Dead로
// 냈다. 사본에 없는 id를 gone으로 세는 것은 TestDead_사본에_없는_id는_까닭_gone이다가 지킨다.
func TestHostpath_지운_기억이_든_묶음은_재개_시작에서_빠지고_Dead가_없다(t *testing.T) {
	f := open(t)
	core := f.p.IDOf("deploy")
	st, err := memory.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pin(core, memory.Who{}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, err := f.env.Recaller.Start("s1", "/bench/app"); err != nil {
		t.Fatal(err)
	}
	st, err = memory.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Forget(memory.ForgetInput{IDs: []string{core}, Reason: "시험"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	again, err := f.env.Recaller.Start("s1", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(again.IDs, core) || strings.Contains(again.Text, core) || len(again.Dead) != 0 {
		t.Fatalf("resumed start: ids %v dead %v why %v, want the bundle rebuilt without core", again.IDs, again.Dead, again.DeadWhy)
	}
}

// id를 하나도 싣지 않은 주입 글(찾는 곳 줄·알림 줄)도 모델 앞에 놓인 글자다: 건너뛴 호출이 아니면 글자로 센다. 세지 않으면
// id 없는 줄을 더 내는 버전의 글자가 적게 잡힌다.
func TestHostpath_id_없는_주입_글도_글자로_센다(t *testing.T) {
	f := open(t)
	got, err := f.env.Recaller.Start("s1", "/bench/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.IDs) != 0 || got.Text == "" || got.Chars != utf8.RuneCountInString(got.Text) {
		t.Fatalf("start without a core: ids %v chars %d text %q, want the text counted", got.IDs, got.Chars, got.Text)
	}
	// 사람이 아닌 글처럼 건너뛴 호출은 여전히 0자다.
	skipped, err := f.env.Recaller.Request("s1", "/bench/app", "<task-notification>끝남</task-notification>")
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Chars != 0 || skipped.Text != "" || len(skipped.IDs) != 0 {
		t.Fatalf("skipped request: %+v", skipped)
	}
}

func TestFinder는_질의의_기억_id를_낸다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Finder.Find("/bench/app", "pnpm", bench.FindLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ids, f.p.IDOf("pnpm")) || slices.Contains(ids, f.p.IDOf("other")) {
		t.Fatalf("ids = %v", ids)
	}
	one, err := hostpath.NewFinder(f.db, mustSet(t)).Find("/bench/app", "배포", 1)
	if err != nil || len(one) != 1 || one[0] != f.p.IDOf("deploy") {
		t.Fatalf("NewFinder ids = %v, err = %v", one, err)
	}
}

// 중복 후보는 실제 저장이 보는 판정(memory.Store.SaveCheck)이다. 같은 제목은 Existing, 비슷한 기억은 Similar로 센다.
func TestSimilar는_저장_판정을_쓰고_이름에_밝힌다(t *testing.T) {
	f := open(t)
	n, ok := f.env.Similar.(interface{ Name() string })
	if !ok || n.Name() != hostpath.SimilarName || hostpath.SimilarName != "save-check" {
		t.Fatalf("similar name = %v", f.env.Similar)
	}
	// 같은 제목(같은 종류·범위): 저장하면 그 기억에 출처가 더해진다. 가리킨 기억으로 센다.
	got, err := f.env.Similar.Similar("/bench/app", bench.Draft{Kind: "fact", Title: "운영 배포는 main 병합으로 일어난다", Scope: "global"})
	if ids := got.Block; err != nil || len(ids) != 1 || ids[0] != f.p.IDOf("deploy") || len(got.Near) != 0 {
		t.Fatalf("same title: %+v, err = %v", got, err)
	}
	// 같은 주제의 현재값: 저장하면 그 기억을 대체한다.
	got, err = f.env.Similar.Similar("/bench/app", bench.Draft{Kind: "rule", Title: "설치 도구는 pnpm 하나", Topic: "앱 패키지 설치", Scope: "project:/bench/app"})
	if ids := got.Block; err != nil || len(ids) != 1 || ids[0] != f.p.IDOf("pnpm") || len(got.Near) != 0 {
		t.Fatalf("same topic: %+v, err = %v", got, err)
	}
}

// find 상위 3을 그대로 내지 않는다: 비슷한 것이 없으면 비어 있다(L-16 판정).
func TestSimilar는_비슷한_것이_없으면_비어_있다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Similar.Similar("/bench/app", bench.Draft{Kind: "fact", Title: "차트 라이브러리는 서버 렌더링에서 크기를 못 잰다", Scope: "global"})
	if err != nil || len(ids.Block) != 0 || len(ids.Near) != 0 {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

func TestSimilar는_다른_범위의_기억을_비슷하다고_세지_않는다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Similar.Similar("/bench/other", bench.Draft{Kind: "fact", Title: "이 앱은 pnpm으로 설치한다 npm install을 쓰지 않는다", Body: "lock 파일은 pnpm-lock.yaml 하나만 둔다.", Scope: "project:/bench/other"})
	if err != nil || len(ids.Block) != 0 || len(ids.Near) != 0 {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}

// 문턱(saveSimilarMin)과 가까운 하한(saveNearMin)은 설정 덮어쓰기로 바꿔 잰다. 막지 않으면 그 기억은 가까운 기억이다.
func TestSimilar는_덮어쓴_saveSimilarMin과_saveNearMin을_따른다(t *testing.T) {
	draft := bench.Draft{Kind: "decision", Title: "운영 배포는 main 병합으로 한다", Body: "감시기가 origin/main을 보고 배포한다.", Scope: "global"}
	meets := func(set map[string]string) bench.SaveMeets {
		f := openWith(t, set)
		got, err := f.env.Similar.Similar("/bench/app", draft)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := meets(nil); len(got.Block) == 0 || len(got.Near) != 0 {
		t.Fatalf("default threshold: want the deploy memory as similar, got %+v", got)
	}
	if got := meets(map[string]string{"saveSimilarMin": "1"}); len(got.Block) != 0 || len(got.Near) == 0 {
		t.Fatalf("saveSimilarMin=1: %+v, want the deploy memory as near", got)
	}
	if got := meets(map[string]string{"saveSimilarMin": "1", "saveNearMin": "1"}); len(got.Block) != 0 || len(got.Near) != 0 {
		t.Fatalf("saveSimilarMin=1 saveNearMin=1: %+v, want none", got)
	}
}

func TestOptions의_덮어쓰기는_설정_검사를_따른다(t *testing.T) {
	for _, tc := range []struct{ key, val string }{{"nope", "1"}, {"saveSimilarMin", "2"}, {"saveSimilarMin", "abc"}, {"recallChars", "5"}} {
		if err := hostpath.CheckSet(map[string]string{tc.key: tc.val}); err == nil {
			t.Errorf("%s=%s was accepted", tc.key, tc.val)
		}
	}
	if err := hostpath.CheckSet(map[string]string{"saveSimilarMin": "0.7", "recallChars": "800", "autoTidy": "false", "language": "ko"}); err != nil {
		t.Errorf("valid set: %v", err)
	}
	set, err := bench.Parse([]byte(setJSON))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostpath.FactoryWith(hostpath.Options{Set: map[string]string{"saveSimilarMin": "2"}})(t.TempDir()+"/x.db", set); err == nil {
		t.Error("the factory opened with a setting the check refuses")
	}
}

// 위임 묶음에서 상시 핵심(A층)의 기억은 핵심 제목 줄 몫(Lead)으로 가른다.
func TestSubagent는_상시_핵심을_Lead로_가른다(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	set, err := bench.Parse([]byte(`{"about":"t","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",
	 "memories":[{"key":"core","kind":"rule","scope":"global","quote":true,"pinned":true,"title":"존댓말로 답한다","topic":"말투","ageDays":9},
	  {"key":"pay","kind":"rule","scope":"global","quote":true,"title":"결제 모듈 동시성 시험은 경합을 실제로 일으킨다","topic":"결제 동시성 시험","body":"결제 모듈 동시성 시험은 경합을 실제로 일으킨다","ageDays":3}],
	 "cases":[{"id":"c1","session":"s1","kind":"subagent","task":"작업"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	db, _ := p.Copy()
	env, err := hostpath.Factory(db, set)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if _, err := env.Recaller.Start("s1", "/bench/app"); err != nil {
		t.Fatal(err)
	}
	got, err := env.Recaller.Subagent("s1", "/bench/app", "작업")
	if err != nil {
		t.Fatal(err)
	}
	core := p.IDOf("core")
	if !slices.Equal(got.IDs, []string{core}) || !slices.Equal(got.Lead, []string{core}) || got.LeadChars != got.Chars || got.Chars == 0 {
		t.Fatalf("subagent = %+v", got)
	}
	// 위임 글에 맞는 기억은 위임 회상 몫이다: 그 id는 Lead가 아니고, 글자도 제목 줄 몫(LeadChars) 밖이다.
	got, err = env.Recaller.Subagent("s1", "/bench/app", "결제 모듈 동시성 시험을 고친다. 경합을 실제로 일으키는 시험을 쓴다")
	if err != nil {
		t.Fatal(err)
	}
	pay := p.IDOf("pay")
	role := string([]rune(got.Text)[max(got.Chars-got.LeadChars, 0):])
	if !slices.Equal(got.IDs, []string{pay, core}) || !slices.Equal(got.Lead, []string{core}) || got.LeadChars == 0 || got.LeadChars >= got.Chars ||
		!strings.HasPrefix(role, inject.RoleHead) {
		t.Fatalf("subagent with a recall = %+v", got)
	}
}

func TestCwd_밖의_폴더에서는_프로젝트_기억을_찾지_않는다(t *testing.T) {
	f := open(t)
	ids, err := f.env.Finder.Find("/bench/other", "pnpm", bench.FindLimit)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ids, f.p.IDOf("pnpm")) {
		t.Fatalf("ids = %v", ids)
	}
}

// 바깥 일(자동 백업·정리)은 하지 않고, 실제 ORBIT_HOME에는 아무것도 쓰지 않는다.
func TestFactory는_홈에_아무것도_쓰지_않는다(t *testing.T) {
	f := open(t)
	if _, err := f.env.Recaller.Start("s4", "/bench/app"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Recaller.Request("s4", "/bench/app", "pnpm"); err != nil {
		t.Fatal(err)
	}
	if err := f.env.Close(); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(testHome)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 0 {
		t.Fatalf("ORBIT_HOME(%s)에 %v가 생겼다", testHome, names)
	}
	_ = filepath.Join
}

func mustSet(t *testing.T) *bench.Set {
	t.Helper()
	set, err := bench.Parse([]byte(setJSON))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// 어댑터 대조: 호스트 계약 경로(Request)는 지금 inject를 직접 부른 것과 같은 id를 낸다(host는 그 위의 얇은 층이다).
// 옛 판(87ab0fb)과 같음은 보지 않는다: 옛 회상은 baseline-legacy.json의 엄격 비교가 지킨다.
func TestRequest가_inject를_직접_부른_것과_같은_id를_낸다(t *testing.T) {
	f := open(t)
	const prompt = "pnpm으로 설치해 줘"
	got, err := f.env.Recaller.Request("s-same", "/bench/app", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.IDs) == 0 {
		t.Fatal("the request recalled nothing: the comparison would prove nothing")
	}
	// 같은 사본을 한 번 더 복사해 inject를 직접 부른다(표시 표식이 섞이지 않게).
	db2, err := f.p.Copy()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := memory.OpenRead(db2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := project.Resolve("/bench/app")
	where := p.Dir
	if where == "" {
		where = p.Scope
	}
	want := inject.PromptInjectionFor(s, config.Default(), where, prompt, "bench", "s-same", inject.Options{})
	if !slices.Equal(got.IDs, want.MemoryIDs) {
		t.Fatalf("host ids = %v, inject ids = %v", got.IDs, want.MemoryIDs)
	}
}

// 사람이 쓴 글이 아니면 회상하지 않는다(사람 요청 판정은 host가 한다).
func TestRequest는_사람이_아닌_글을_회상하지_않는다(t *testing.T) {
	f := open(t)
	got, err := f.env.Recaller.Request("s3", "/bench/app", "<task-notification>\n<summary> pnpm 설치가 끝났다 </summary>\n</task-notification>")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.IDs) != 0 || got.Chars != 0 {
		t.Fatalf("a task notification was recalled: %+v", got)
	}
}

// 재생의 도구 사례는 사례의 input을 계약의 toolInput으로 넘긴다(명세 T-1·T-4). --set으로 켠 도구 입력 문이 그 글로 판정해, 후보
// 교훈은 관련 없는 명령에서 빠지고 관련 있는 명령에서 든다. 기본은 켠 상태이고, 끄면 지금처럼 도구 이름만으로 든다.
func TestTool은_사례_입력을_toolInput으로_넘기고_set의_도구_입력_문을_따른다(t *testing.T) {
	const toolSet = `{"about":"t","now":"2026-11-03T09:00:00+09:00","cwd":"/bench/app","seed":"s",
"memories":[{"key":"lock","kind":"lesson","scope":"global","title":"pnpm install은 lock 파일을 고친다","body":"설치 뒤 pnpm-lock.yaml 변경을 확인한다","tools":["Bash"],"ageDays":1}],
"cases":[{"id":"c1","origin":"user","kind":"request","prompt":"설치","expect":[]}]}`
	set, err := bench.Parse([]byte(toolSet))
	if err != nil {
		t.Fatal(err)
	}
	p, err := bench.Prepare(set, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	lock := p.IDOf("lock")
	gate := map[string]string{"toolInputGate": "true", "toolInputMinScore": "0.3"}
	for _, tc := range []struct {
		set     map[string]string
		session string
		input   string
		want    bool
	}{
		{gate, "a", "ls -la", false},
		{gate, "b", "pnpm install", true},
		{nil, "c", "ls -la", false},
		{map[string]string{"toolInputGate": "false"}, "d", "ls -la", true},
	} {
		db, err := p.Copy()
		if err != nil {
			t.Fatal(err)
		}
		env, err := hostpath.FactoryWith(hostpath.Options{Set: tc.set})(db, set)
		if err != nil {
			t.Fatal(err)
		}
		got, err := env.Recaller.Tool(tc.session, "/bench/app", "Bash", tc.input)
		env.Close()
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(got.IDs, lock) != tc.want {
			t.Errorf("set %v input %q: ids %v, want the lesson %v", tc.set, tc.input, got.IDs, tc.want)
		}
	}
}
