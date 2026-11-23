package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 찾기 질의의 조사 떼기 스위치(명세 Q-2): 기본은 꺼짐이고, 이 키가 없는 옛 설정 파일도 꺼짐으로 읽고, 파일에 true를 적으면
// 켜진다(검증할 범위는 없다).
func TestFindStripParticles_기본은_꺼짐이고_옛_설정_파일도_꺼짐이다(t *testing.T) {
	if Default().FindStripParticles {
		t.Fatal("the default is on")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full","recallMinScore":0.42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.FindStripParticles {
		t.Fatalf("old file = %v err %v", c.FindStripParticles, err)
	}
	if err := os.WriteFile(p, []byte(`{"findStripParticles":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.FindStripParticles {
		t.Fatalf("file false = %v err %v", c.FindStripParticles, err)
	}
	if err := os.WriteFile(p, []byte(`{"findStripParticles":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || !c.FindStripParticles {
		t.Fatalf("file true = %v err %v", c.FindStripParticles, err)
	}
}

// 저장하면 켬이 파일에 남아 다시 읽어도 켬이다.
func TestFindStripParticles_켬은_저장하고_다시_읽어도_켬이다(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	c := Default()
	c.FindStripParticles = true
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(p); err != nil || !got.FindStripParticles {
		t.Fatalf("after save = %v err %v", got.FindStripParticles, err)
	}
}
