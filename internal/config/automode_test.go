package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 자동 모드(명세 C-9): 기본은 꺼짐이고, 키가 없는 옛 설정 파일도 꺼짐으로 읽으며, 파일의 autoMode를 그대로 읽고 저장한다.
func TestAutoMode_기본은_꺼짐이고_옛_설정_파일도_꺼짐이다(t *testing.T) {
	if Default().AutoMode {
		t.Fatal("기본이 켜짐")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full","recallMinScore":0.42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.AutoMode {
		t.Fatalf("old file = autoMode %v err %v", c.AutoMode, err)
	}
	if err := os.WriteFile(p, []byte(`{"autoMode":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || !c.AutoMode {
		t.Fatalf("file = autoMode %v err %v", c.AutoMode, err)
	}
	c.AutoMode = false
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.AutoMode {
		t.Fatalf("saved = autoMode %v err %v", c.AutoMode, err)
	}
}
