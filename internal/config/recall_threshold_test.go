package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 요청 회상의 두 문턱(설계 · 지연 회상 5.1 4단계): 찾기 점수 recallMinScore와 뜻 있는 낱말 중 맞은 비율 recallMinCoverage.
// 상대 문턱 recallMinRelative(설계 보완, 1-6 b6)는 두 문턱을 넘은 기억 가운데 1위 점수의 몇 배 이상만 남기는지다.
// 0 < x ≤ 1만 받고, 키가 없는 옛 파일은 기본값으로 읽는다.
func TestRecallThresholds_기본값과_범위(t *testing.T) {
	d := Default()
	if d.RecallMinRelative != DefaultRecallMinRelative || !(DefaultRecallMinRelative > 0 && DefaultRecallMinRelative <= 1) {
		t.Fatalf("relative default = %v", d.RecallMinRelative)
	}
	if d.RecallMinScore != DefaultRecallMinScore || d.RecallMinCoverage != DefaultRecallMinCoverage {
		t.Fatalf("default = %v %v", d.RecallMinScore, d.RecallMinCoverage)
	}
	if !(DefaultRecallMinScore > 0 && DefaultRecallMinScore <= 1 && DefaultRecallMinCoverage > 0 && DefaultRecallMinCoverage <= 1) {
		t.Fatalf("defaults out of range: %v %v", DefaultRecallMinScore, DefaultRecallMinCoverage)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.RecallMinScore != DefaultRecallMinScore || c.RecallMinCoverage != DefaultRecallMinCoverage || c.RecallMinRelative != DefaultRecallMinRelative {
		t.Fatalf("old file = %v %v %v", c.RecallMinScore, c.RecallMinCoverage, err)
	}
	for _, key := range []string{"recallMinScore", "recallMinCoverage", "recallMinRelative"} {
		set := func(c *Config, v float64) {
			*map[string]*float64{"recallMinScore": &c.RecallMinScore, "recallMinCoverage": &c.RecallMinCoverage, "recallMinRelative": &c.RecallMinRelative}[key] = v
		}
		for _, bad := range []float64{0, -0.1, 1.01, math.NaN()} {
			c := Default()
			set(&c, bad)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s %v accepted or unclear: %v", key, bad, err)
			}
		}
		for _, good := range []float64{0.01, 0.5, 1} {
			c := Default()
			set(&c, good)
			if err := c.Validate(); err != nil {
				t.Errorf("%s %v: %v", key, good, err)
			}
		}
	}
	if err := os.WriteFile(p, []byte(`{"recallMinScore":0.42,"recallMinCoverage":0.6}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.RecallMinScore != 0.42 || c.RecallMinCoverage != 0.6 {
		t.Fatalf("file = %v %v %v", c.RecallMinScore, c.RecallMinCoverage, err)
	}
}

// 요청 회상의 글자 수는 recallChars와 요청 회상 상한(500자) 중 작은 값이다.
func TestPromptRecallChars는_recallChars와_500자_중_작은_값이다(t *testing.T) {
	c := Default()
	if got := c.PromptRecallChars(); got != MaxPromptRecallChars || MaxPromptRecallChars != 500 {
		t.Fatalf("default = %d (max %d)", got, MaxPromptRecallChars)
	}
	c.RecallChars = 200
	if got := c.PromptRecallChars(); got != 200 {
		t.Fatalf("200 → %d", got)
	}
	c.RecallChars = 0
	if got := c.PromptRecallChars(); got != MaxPromptRecallChars {
		t.Fatalf("0 → %d", got)
	}
}
