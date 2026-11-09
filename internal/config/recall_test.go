package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecallMode_옛_inject만_있는_파일은_같은_뜻으로_읽는다(t *testing.T) {
	for inject, want := range map[string]string{"full": RecallAuto, "rules": RecallOnDemand, "off": RecallOff} {
		c, err := Load(writeCfg(t, `{"inject":"`+inject+`"}`))
		if err != nil || c.RecallMode() != want {
			t.Fatalf("inject=%s: mode=%q err=%v want %q", inject, c.RecallMode(), err, want)
		}
	}
}

func TestRecallMode_recall만_있으면_그_값이고_빈_파일과_기본값은_auto(t *testing.T) {
	c, err := Load(writeCfg(t, `{"recall":"off"}`))
	if err != nil || c.RecallMode() != RecallOff {
		t.Fatalf("recall만: %q %v", c.RecallMode(), err)
	}
	c, err = Load(writeCfg(t, `{}`))
	if err != nil || c.RecallMode() != RecallAuto {
		t.Fatalf("빈 파일: %q %v", c.RecallMode(), err)
	}
	if Default().RecallMode() != RecallAuto || Default().Recall != "" {
		t.Fatalf("기본값: %+v", Default())
	}
}

func TestRecallMode_둘이_다르면_더_제한적인_값(t *testing.T) {
	cases := []struct{ recall, inject, want string }{
		{"auto", "rules", RecallOnDemand},
		{"on_demand", "full", RecallOnDemand},
		{"off", "full", RecallOff},
		{"auto", "off", RecallOff},
		{"on_demand", "off", RecallOff},
		{"off", "rules", RecallOff},
		{"auto", "full", RecallAuto},
	}
	for _, tc := range cases {
		c, err := Load(writeCfg(t, `{"recall":"`+tc.recall+`","inject":"`+tc.inject+`"}`))
		if err != nil || c.RecallMode() != tc.want {
			t.Fatalf("%+v: mode=%q err=%v", tc, c.RecallMode(), err)
		}
	}
}

func TestValidate_잘못된_recall_값은_거절(t *testing.T) {
	c := Default()
	c.Recall = "sometimes"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "recall") {
		t.Fatalf("err = %v", err)
	}
	for _, ok := range []string{"", "auto", "on_demand", "off"} {
		c.Recall = ok
		if err := c.Validate(); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
}

func TestSave_recall과_inject_두_키가_모두_남는다(t *testing.T) {
	p := writeCfg(t, `{"inject":"rules"}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["recall"] != "on_demand" || m["inject"] != "rules" {
		t.Fatalf("file = %s", b)
	}
	got, err := Load(p)
	if err != nil || got.RecallMode() != RecallOnDemand {
		t.Fatalf("got %+v %v", got, err)
	}
}
