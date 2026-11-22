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

func requestTexts(s Session) []string {
	var o []string
	for _, st := range s.Steps {
		if st.Kind == KindRequest {
			o = append(o, st.Text)
		}
	}
	return o
}

func TestSelect_같은_세션_id의_앞부분_사본은_긴_쪽_하나만_쓴다(t *testing.T) {
	root := t.TempDir()
	short := uuidRec("dup", "u1", "2026-11-01T10:00:00Z", "하나") + uuidRec("dup", "u2", "2026-11-01T10:01:00Z", "둘")
	long := short + uuidRec("dup", "u3", "2026-11-01T10:02:00Z", "셋")
	writeFile(t, filepath.Join(root, "pa", "copy.jsonl"), short)
	writeFile(t, filepath.Join(root, "pb", "orig.jsonl"), long)
	got, err := Select([]string{root}, Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Join(requestTexts(got[0]), ",") != "하나,둘,셋" {
		t.Fatalf("got %d sessions %+v", len(got), got)
	}
}

func TestSelect_같은_세션_id의_갈라진_기록은_uuid로_합쳐_시각순이다(t *testing.T) {
	root := t.TempDir()
	base := uuidRec("fork", "u1", "2026-11-01T10:00:00Z", "공통1") + uuidRec("fork", "u2", "2026-11-01T10:01:00Z", "공통2")
	a := base + uuidRec("fork", "a1", "2026-11-01T10:05:00Z", "가지A")
	b := base + uuidRec("fork", "b1", "2026-11-01T10:03:00Z", "가지B1") + uuidRec("fork", "b2", "2026-11-01T10:07:00Z", "가지B2")
	// 파일 이름 순서가 결과에 영향을 주면 안 된다.
	writeFile(t, filepath.Join(root, "p1", "z.jsonl"), a)
	writeFile(t, filepath.Join(root, "p1", "a.jsonl"), b)
	got, err := Select([]string{root}, Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Join(requestTexts(got[0]), ",") != "공통1,공통2,가지B1,가지A,가지B2" {
		t.Fatalf("got %d sessions %+v", len(got), got)
	}
}

func TestSelect_같은_세션_id가_두_기록_폴더에_있어도_하나다(t *testing.T) {
	r1, r2 := t.TempDir(), t.TempDir()
	rec := uuidRec("x", "u1", "2026-11-01T10:00:00Z", "요청")
	writeFile(t, filepath.Join(r1, "p", "x.jsonl"), rec)
	writeFile(t, filepath.Join(r2, "q", "x.jsonl"), rec+uuidRec("x", "u2", "2026-11-01T10:01:00Z", "뒤"))
	got, err := Select([]string{r1, r2}, Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Join(requestTexts(got[0]), ",") != "요청,뒤" {
		t.Fatalf("got %d sessions %+v", len(got), got)
	}
}

func TestSelect_세션_시작_단계를_두_사본_어느_쪽에서도_잃지_않는다(t *testing.T) {
	root := t.TempDir()
	start := `{"type":"attachment","uuid":"h1","sessionId":"s","cwd":"/work/a","timestamp":"2026-11-01T09:59:00Z","attachment":{"hookEvent":"SessionStart","hookName":"SessionStart:startup"}}` + "\n"
	writeFile(t, filepath.Join(root, "p", "a.jsonl"), start+uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나"))
	writeFile(t, filepath.Join(root, "p", "b.jsonl"), start+uuidRec("s", "u1", "2026-11-01T10:00:00Z", "하나")+uuidRec("s", "u2", "2026-11-01T10:01:00Z", "둘"))
	got, err := Select([]string{root}, Options{All: true})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
	if st := got[0].Steps; len(st) != 3 || st[0].Kind != KindStart {
		t.Fatalf("steps %+v", st)
	}
}
