package retro

import (
	"errors"
	"testing"
	"time"

	"github.com/onidot-labs/orbit/internal/i18n"
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
	if ErrBusy.Error() != "다른 회고가 이미 돌고 있습니다" || !errors.Is(ErrBusy, ErrBusy) {
		t.Fatalf("busy = %v", ErrBusy)
	}
}
