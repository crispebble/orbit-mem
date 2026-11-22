package main

import (
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
)

// 자동 모드(명세 C-9)는 설정 명령으로 켜고 끈다. 기본은 꺼짐이고, 연 저장소의 정책에 실린다.
func TestAutoMode_설정_명령으로_켜고_끄며_저장소_정책에_실린다(t *testing.T) {
	home(t)
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	if s.Policy.AutoMode {
		t.Fatal("기본이 켜짐")
	}
	s.Close()
	if _, errOut, code := run(t, "", "config", "set", "autoMode", "true"); code != 0 {
		t.Fatalf("set: %s", errOut)
	}
	if _, _, code := run(t, "", "config", "set", "autoMode", "maybe"); code == 0 {
		t.Fatal("참·거짓이 아닌 값을 받았습니다")
	}
	s, err = openStore()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Policy.AutoMode {
		t.Fatalf("policy = %+v", s.Policy)
	}
	s.Close()
	if !policyOf(config.Config{AutoMode: true}).AutoMode || policyOf(config.Config{}).AutoMode {
		t.Fatal("policyOf가 autoMode를 옮기지 않는다")
	}
	if _, errOut, code := run(t, "", "config", "set", "autoMode", "false"); code != 0 {
		t.Fatalf("set false: %s", errOut)
	}
	if cfg, err := loadConfig(); err != nil || cfg.AutoMode {
		t.Fatalf("끈 뒤 %v %v", cfg.AutoMode, err)
	}
}
