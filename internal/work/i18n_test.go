package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

func TestOpen_깨진_파일_오류는_언어를_따른다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "work.db")
	if err := os.WriteFile(p, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(p)
	if err == nil || !strings.Contains(err.Error(), "cannot open the work log DB") {
		t.Fatalf("en = %v", err)
	}
	i18n.Set(i18n.KO)
	defer i18n.Set(i18n.EN)
	_, err = Open(p)
	if err == nil || !strings.Contains(err.Error(), "작업 기록 DB를 열 수 없습니다") {
		t.Fatalf("ko = %v", err)
	}
}
