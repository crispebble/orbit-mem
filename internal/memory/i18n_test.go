package memory

import (
	"errors"
	"strings"
	"testing"

	"github.com/onidot-labs/openorbit/internal/i18n"
)

func TestErrors_언어를_따른다(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Get("m-none")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "no such memory") {
		t.Fatalf("en = %q", err)
	}
	i18n.Set(i18n.KO)
	defer i18n.Set(i18n.EN)
	_, err = s.Get("m-none")
	if !strings.HasPrefix(err.Error(), "기억이 없습니다") {
		t.Fatalf("ko = %q", err)
	}
	werr := i18n.Errorf("memory.err.link_self", ErrInvalid)
	if !errors.Is(werr, ErrInvalid) || !strings.Contains(werr.Error(), "자기 자신") {
		t.Fatalf("wrapped = %v", werr)
	}
}
