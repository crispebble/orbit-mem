package claudelog

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// noSIDRec는 sessionId가 없는 사람 요청 기록 한 줄이다(uuid는 있다).
func noSIDRec(uuid, at, text string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"cwd":"/work/a","timestamp":%q,"origin":{"kind":"human"},"message":{"role":"user","content":%q}}`+"\n", uuid, at, text)
}

// id 없는 기록은 파일에서 바로 앞 id에 속한다: 이어받은 파일의 꼬리에 낀 id 없는 기록은 새 세션의 단계다.
func TestSelect_id_없는_기록은_앞_기록의_id를_이어받는다(t *testing.T) {
	root := t.TempDir()
	prefix := uuidRec("p", "u1", "2026-11-01T10:00:00Z", "공통")
	writeFile(t, filepath.Join(root, "x", "p.jsonl"), prefix)
	writeFile(t, filepath.Join(root, "x", "c.jsonl"), prefix+uuidRec("c", "c1", "2026-11-01T10:01:00Z", "새1")+noSIDRec("c2", "2026-11-01T10:02:00Z", "새2 id 없음"))
	m := selectAll(t, root)
	if got := strings.Join(requestTexts(m["c"]), ","); got != "새1,새2 id 없음" {
		t.Fatalf("새 세션 %q", got)
	}
	if got := strings.Join(requestTexts(m["p"]), ","); got != "공통" {
		t.Fatalf("원래 세션 %q", got)
	}
	if m["c"].UnkeyedSteps != 1 {
		t.Errorf("id 없이 단계를 만든 기록 %d, want 1", m["c"].UnkeyedSteps)
	}
}

// 파일의 첫 id보다 앞에 있는 id 없는 기록은 그 파일의 첫 id에 속한다. 파일 구성(혼자인지 합쳐지는지)에 따라 남거나
// 빠지지 않는다.
func TestSelect_첫_id_앞의_id_없는_기록은_그_파일의_첫_id에_속한다(t *testing.T) {
	lead := noSIDRec("n0", "2026-11-01T09:59:00Z", "맨 앞 id 없음")
	one := t.TempDir()
	writeFile(t, filepath.Join(one, "p", "s.jsonl"), lead+uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나"))
	merged := t.TempDir()
	writeFile(t, filepath.Join(merged, "p", "s.jsonl"), lead+uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나"))
	writeFile(t, filepath.Join(merged, "q", "s.jsonl"), uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나")+uuidRec("s", "u2", "2026-11-01T10:01:00Z", "둘"))
	a, b := selectAll(t, one)["s"], selectAll(t, merged)["s"]
	if got := strings.Join(requestTexts(a), ","); got != "맨 앞 id 없음,하나" {
		t.Fatalf("혼자인 파일: %q", got)
	}
	if got := strings.Join(requestTexts(b), ","); got != "맨 앞 id 없음,하나,둘" {
		t.Fatalf("합친 파일: %q", got)
	}
}

// 기록에 id가 전혀 없어 파일 이름이 id를 대신하는 파일은, 기록 안 id가 그 이름과 같은 파일과 묶여도 기록을 잃지 않는다.
func TestSelect_이름이_id를_대신하는_파일도_같은_id_파일과_묶인다(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "p", "s.jsonl"), noSIDRec("n1", "2026-11-01T10:00:00Z", "이름 파일"))
	writeFile(t, filepath.Join(root, "q", "other.jsonl"), uuidRec("s", "u2", "2026-11-01T10:01:00Z", "기록 id 파일"))
	m := selectAll(t, root)
	if got := strings.Join(requestTexts(m["s"]), ","); got != "이름 파일,기록 id 파일" {
		t.Fatalf("%q (세션 %d개)", got, len(m))
	}
}

// ReadMerged와 Select는 경로·기록 폴더를 준 순서와 되풀이에 기대지 않는다.
func TestReadMerged_경로_순서에_기대지_않는다(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "p", "a.jsonl"), filepath.Join(root, "q", "b.jsonl")
	const at = "2026-11-01T10:00:00Z"
	writeFile(t, a, uuidRec("s", "a1", at, "a"))
	writeFile(t, b, uuidRec("s", "b1", at, "b"))
	x, err1 := ReadMerged([]string{a, b}, "s")
	y, err2 := ReadMerged([]string{b, a}, "s")
	if err1 != nil || err2 != nil || !reflect.DeepEqual(x, y) {
		t.Fatalf("%v %v\n%+v\n%+v", err1, err2, x, y)
	}
	if got := strings.Join(requestTexts(x), ","); got != "a,b" {
		t.Fatalf("%q", got)
	}
}

func TestSelect_기록_폴더를_거꾸로_되풀이해_줘도_결과가_같다(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	prefix := uuidRec("p", "u1", "2026-11-01T10:00:00Z", "공통")
	writeFile(t, filepath.Join(r1, "x", "p.jsonl"), prefix)
	writeFile(t, filepath.Join(r2, "y", "p.jsonl"), prefix+uuidRec("p", "u2", "2026-11-01T10:01:00Z", "뒤"))
	writeFile(t, filepath.Join(r2, "y", "c.jsonl"), prefix+uuidRec("c", "c1", "2026-11-01T10:02:00Z", "새"))
	opts := Options{All: true}
	want, err := Select([]string{r1, r2}, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, roots := range [][]string{{r2, r1}, {r1, r2, r1, r2}, {r1, r1, r2}} {
		got, err := Select(roots, opts)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: %v\n got %+v\nwant %+v", roots, err, got, want)
		}
	}
}

// 못 읽은 파일: 묶인 세션은 unreadFiles로, 세션을 못 만든 파일은 보고에 센다.
func TestSelectReport_세션을_못_만든_파일을_센다(t *testing.T) {
	root := t.TempDir()
	makeSession(t, root, "p", "ok", "/work/a", 5, 2)
	// 디렉터리는 glob에 걸리지만 읽을 수 없다.
	if err := os.MkdirAll(filepath.Join(root, "p", "dir.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, rep, err := SelectReport([]string{root}, Options{All: true})
	if err != nil || len(got) != 1 || rep.LostFiles != 1 {
		t.Fatalf("%v %d %+v", err, len(got), rep)
	}
}
