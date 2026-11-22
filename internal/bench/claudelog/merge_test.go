package claudelog

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// uuidRec는 uuid가 있는 사람 요청 기록 한 줄이다.
func uuidRec(sid, uuid, at, text string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"sessionId":%q,"cwd":"/work/a","timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":%q}}`+"\n", uuid, sid, at, text)
}

// startRec는 uuid가 있는 세션 시작 훅 기록 한 줄이다.
func startRec(sid, uuid, at, source string) string {
	return fmt.Sprintf(`{"type":"attachment","uuid":%q,"sessionId":%q,"cwd":"/work/a","timestamp":%q,"attachment":{"hookEvent":"SessionStart","hookName":"SessionStart:%s"}}`+"\n", uuid, sid, at, source)
}

func requestTexts(s Session) []string {
	var o []string
	for _, st := range s.Steps {
		if st.Kind == KindRequest {
			o = append(o, st.Text)
		}
	}
	return o
}

func byID(t *testing.T, ss []Session) map[string]Session {
	t.Helper()
	m := map[string]Session{}
	for _, s := range ss {
		if _, dup := m[s.ID]; dup {
			t.Fatalf("세션 id %q가 둘이다", s.ID)
		}
		m[s.ID] = s
	}
	return m
}

func selectAll(t *testing.T, roots ...string) map[string]Session {
	t.Helper()
	got, err := Select(roots, Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	return byID(t, got)
}

// 이어받아 만든 파일은 앞부분(복사된 기록)이 원래 세션 id를, 꼬리가 새 id(파일 이름)를 달고 있다. 훅은 그때 각 id로 따로
// 돌았으므로 재생도 둘을 따로 세고, 복사된 기록은 원래 세션에 한 번만 센다.
func TestSelect_이어받은_파일은_원래_세션과_새_세션으로_가른다(t *testing.T) {
	root := t.TempDir()
	prefix := uuidRec("p", "u1", "2026-11-01T10:00:00Z", "공통1") + uuidRec("p", "u2", "2026-11-01T10:01:00Z", "공통2")
	writeFile(t, filepath.Join(root, "pa", "p.jsonl"), prefix+uuidRec("p", "u3", "2026-11-01T10:05:00Z", "원래 뒤"))
	writeFile(t, filepath.Join(root, "pa", "c.jsonl"), prefix+uuidRec("c", "c1", "2026-11-01T10:03:00Z", "가지1")+uuidRec("c", "c2", "2026-11-01T10:07:00Z", "가지2"))
	m := selectAll(t, root)
	if len(m) != 2 {
		t.Fatalf("세션 %d개", len(m))
	}
	if got := strings.Join(requestTexts(m["p"]), ","); got != "공통1,공통2,원래 뒤" {
		t.Errorf("원래 세션: %s", got)
	}
	if got := strings.Join(requestTexts(m["c"]), ","); got != "가지1,가지2" {
		t.Errorf("새 세션: %s", got)
	}
	if m["p"].Merge != MergeCopy || m["p"].Files != 2 || m["p"].Parent != "" {
		t.Errorf("원래 세션 표시 %+v", m["p"])
	}
	if m["c"].Parent != "p" || m["c"].Merge != "" || m["c"].Files != 0 {
		t.Errorf("새 세션 표시: parent %q merge %q files %d", m["c"].Parent, m["c"].Merge, m["c"].Files)
	}
}

func TestSelect_같은_세션_id의_앞부분_사본은_긴_쪽_하나만_쓴다(t *testing.T) {
	root := t.TempDir()
	short := uuidRec("dup", "u1", "2026-11-01T10:00:00Z", "하나") + uuidRec("dup", "u2", "2026-11-01T10:01:00Z", "둘")
	long := short + uuidRec("dup", "u3", "2026-11-01T10:02:00Z", "셋")
	writeFile(t, filepath.Join(root, "pa", "copy.jsonl"), short)
	writeFile(t, filepath.Join(root, "pb", "orig.jsonl"), long)
	m := selectAll(t, root)
	if len(m) != 1 || strings.Join(requestTexts(m["dup"]), ",") != "하나,둘,셋" || m["dup"].Merge != MergeCopy {
		t.Fatalf("%+v", m)
	}
}

func TestSelect_같은_세션_id가_두_기록_폴더에_있어도_하나다(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	rec := uuidRec("x", "u1", "2026-11-01T10:00:00Z", "요청")
	writeFile(t, filepath.Join(r1, "p", "x.jsonl"), rec)
	writeFile(t, filepath.Join(r2, "q", "x.jsonl"), rec+uuidRec("x", "u2", "2026-11-01T10:01:00Z", "뒤"))
	m := selectAll(t, r1, r2)
	if len(m) != 1 || strings.Join(requestTexts(m["x"]), ",") != "요청,뒤" {
		t.Fatalf("%+v", m)
	}
}

// 같은 id인데 서로 다른 기록을 가진 두 파일: 합집합(uuid 한 번씩)을 시각순으로 세운다. 파일 이름 순서에 기대지 않는다.
func TestSelect_같은_id의_서로_다른_기록은_합집합을_시각순으로_세운다(t *testing.T) {
	root := t.TempDir()
	base := uuidRec("f", "u1", "2026-11-01T10:00:00Z", "공통")
	a := base + uuidRec("f", "a1", "2026-11-01T10:05:00Z", "A")
	b := base + uuidRec("f", "b1", "2026-11-01T10:03:00Z", "B1") + uuidRec("f", "b2", "2026-11-01T10:07:00Z", "B2")
	writeFile(t, filepath.Join(root, "p1", "z.jsonl"), a)
	writeFile(t, filepath.Join(root, "p1", "a.jsonl"), b)
	m := selectAll(t, root)
	if got := strings.Join(requestTexts(m["f"]), ","); got != "공통,B1,A,B2" || m["f"].Merge != MergeUnion {
		t.Fatalf("%s %+v", got, m["f"].Merge)
	}
}

// 시각이 같은 두 기록은 파일 경로순, 같은 파일 안에서는 파일 안 위치순이다.
func TestSelect_시각이_같은_기록은_파일_경로순_위치순이다(t *testing.T) {
	root := t.TempDir()
	const at = "2026-11-01T10:00:00Z"
	writeFile(t, filepath.Join(root, "p", "b.jsonl"), uuidRec("t", "b1", at, "b-첫째")+uuidRec("t", "b2", at, "b-둘째"))
	writeFile(t, filepath.Join(root, "p", "a.jsonl"), uuidRec("t", "a1", at, "a-첫째")+uuidRec("t", "a2", at, "a-둘째"))
	m := selectAll(t, root)
	if got := strings.Join(requestTexts(m["t"]), ","); got != "a-첫째,a-둘째,b-첫째,b-둘째" {
		t.Fatalf("%s", got)
	}
}

// 시각이 없는 기록은 같은 파일의 앞 기록 뒤에 남는다.
func TestSelect_시각_없는_기록은_앞_기록의_시각을_잇는다(t *testing.T) {
	root := t.TempDir()
	noTime := `{"type":"user","uuid":"n1","sessionId":"t","cwd":"/work/a","origin":{"kind":"human"},"message":{"role":"user","content":"시각 없음"}}` + "\n"
	writeFile(t, filepath.Join(root, "p", "a.jsonl"), uuidRec("t", "a1", "2026-11-01T10:00:00Z", "앞")+noTime)
	writeFile(t, filepath.Join(root, "p", "b.jsonl"), uuidRec("t", "b1", "2026-11-01T10:00:30Z", "뒤"))
	m := selectAll(t, root)
	if got := strings.Join(requestTexts(m["t"]), ","); got != "앞,시각 없음,뒤" {
		t.Fatalf("%s", got)
	}
}

// uuid가 다른 시작 훅 기록이 가까우면(startFoldGap 안) 합친 세션에서도 하나로 접힌다.
func TestSelect_합친_세션에서_uuid가_다른_가까운_시작_훅은_하나로_접힌다(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "p", "a.jsonl"), startRec("s", "h1", "2026-11-01T10:00:00Z", "resume")+uuidRec("s", "u1", "2026-11-01T10:01:00Z", "요청"))
	writeFile(t, filepath.Join(root, "p", "b.jsonl"), startRec("s", "h2", "2026-11-01T10:00:02Z", "resume"))
	m := selectAll(t, root)
	starts := 0
	for _, st := range m["s"].Steps {
		if st.Kind == KindStart {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("시작 %d개, want 1", starts)
	}
}

// 합친 세션에도 세션당 요청 상한 뒤의 시작 단계가 남는다.
func TestSelect_합친_세션도_요청_상한_뒤의_시작을_남긴다(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "p", "a.jsonl"), uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나")+uuidRec("s", "u2", "2026-11-01T10:01:00Z", "둘"))
	writeFile(t, filepath.Join(root, "p", "b.jsonl"), uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나")+startRec("s", "h", "2026-11-01T11:00:00Z", "compact"))
	got, err := Select([]string{root}, Options{MaxRequestsPerSession: 1, MinSessions: 1, MinRequests: 1})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
	var kinds []Kind
	for _, st := range got[0].Steps {
		kinds = append(kinds, st.Kind)
	}
	if len(kinds) != 2 || kinds[0] != KindRequest || kinds[1] != KindStart {
		t.Fatalf("단계 %v", kinds)
	}
}

func TestSelect_세션_시작_단계를_두_사본_어느_쪽에서도_잃지_않는다(t *testing.T) {
	root := t.TempDir()
	start := startRec("s", "h1", "2026-11-01T09:59:00Z", "startup")
	writeFile(t, filepath.Join(root, "p", "a.jsonl"), start+uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나"))
	writeFile(t, filepath.Join(root, "p", "b.jsonl"), start+uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나")+uuidRec("s", "u2", "2026-11-01T10:01:00Z", "둘"))
	m := selectAll(t, root)
	if st := m["s"].Steps; len(st) != 3 || st[0].Kind != KindStart {
		t.Fatalf("steps %+v", st)
	}
}

func TestReadMerged_경로가_없으면_오류다(t *testing.T) {
	if _, err := ReadMerged(nil, "x"); err == nil {
		t.Fatal("빈 경로가 통과했다")
	}
}

// 묶음 안 파일 하나를 못 읽어도 나머지로 세션을 만들고, 못 읽은 수를 남긴다. 모두 못 읽으면 오류다.
func TestReadMerged_못_읽은_파일은_세고_나머지로_읽는다(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "g", "s.jsonl")
	writeFile(t, good, uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나"))
	bad := filepath.Join(root, "b", "nope.jsonl") // 없는 파일
	s, err := ReadMerged([]string{good, bad}, "s")
	if err != nil || s.UnreadFiles != 1 || len(requestTexts(s)) != 1 {
		t.Fatalf("%v %+v", err, s)
	}
	if _, err := ReadMerged([]string{bad}, "s"); err == nil {
		t.Fatal("모두 못 읽었는데 오류가 아니다")
	}
}
