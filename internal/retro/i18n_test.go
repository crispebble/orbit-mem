package retro

import (
	"testing"
	"time"

	"github.com/onidot-labs/orbit-mem/internal/i18n"
)

func TestErrors_언어를_따른다(t *testing.T) {
	e := setup(t, time.Date(2026, 10, 28, 9, 0, 0, 0, kst))
	_, err := getStatus(e.s, "r-none")
	if err == nil || err.Error() != "no such retrospective: r-none" {
		t.Fatalf("en = %v", err)
	}
	i18n.Set(i18n.KO)
	defer i18n.Set(i18n.EN)
	_, err = getStatus(e.s, "r-none")
	if err == nil || err.Error() != "회고가 없습니다: r-none" {
		t.Fatalf("ko = %v", err)
	}
	if _, err := e.r.Prepare("monthly", "2026-10-27", "claude"); err == nil || err.Error() != "회고 종류는 daily 또는 weekly입니다" {
		t.Fatalf("kind = %v", err)
	}
}
