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

// 기본값은 실제로 쓰는 값(500)이고, 옛 기본값 1000을 적은 파일도 그대로 읽혀 500으로 동작한다(1-6 b10).
func TestRecallChars_기본값은_실효_상한이고_옛_파일의_1000도_읽는다(t *testing.T) {
	if d := Default(); d.RecallChars != MaxPromptRecallChars {
		t.Fatalf("default recallChars %d, 실효 %d", d.RecallChars, d.PromptRecallChars())
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"recallChars":1000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.RecallChars != 1000 || c.PromptRecallChars() != MaxPromptRecallChars {
		t.Fatalf("old file = %d → %d, %v", c.RecallChars, c.PromptRecallChars(), err)
	}
}

// 맞은 낱말이 하나뿐인 기억의 맞은 비율 문턱 recallMinCoverageOneWord(설계 보완, 1-6 b6 R1)와 위임 회상의 세 문턱
// taskRecallMinScore·taskRecallMinWords·taskRecallMinRelative(T1). 비율·점수는 0 < x ≤ 1, 낱말 수는 2~40(요청 회상이 요구하는 낱말 수 2 ~ 요청
// 낱말 상한)만 받고, 키가 없는 옛 파일은 기본값으로 읽는다.
func TestRecallThresholds_한_낱말_비율과_위임_문턱의_기본값과_범위(t *testing.T) {
	d := Default()
	if d.RecallMinCoverageOneWord != DefaultRecallMinCoverageOneWord || d.TaskRecallMinScore != DefaultTaskRecallMinScore ||
		d.TaskRecallMinWords != DefaultTaskRecallMinWords || d.TaskRecallMinRelative != DefaultTaskRecallMinRelative ||
		d.TaskRecallMinTitleShare != DefaultTaskRecallMinTitleShare {
		t.Fatalf("default = %+v", d)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"inject":"full","recallMinCoverage":0.25}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.RecallMinCoverageOneWord != DefaultRecallMinCoverageOneWord || c.TaskRecallMinWords != DefaultTaskRecallMinWords ||
		c.TaskRecallMinScore != DefaultTaskRecallMinScore || c.TaskRecallMinRelative != DefaultTaskRecallMinRelative ||
		c.TaskRecallMinTitleShare != DefaultTaskRecallMinTitleShare {
		t.Fatalf("old file = %+v %v", c, err)
	}
	floats := map[string]func(*Config) *float64{
		"recallMinCoverageOneWord": func(c *Config) *float64 { return &c.RecallMinCoverageOneWord },
		"taskRecallMinScore":       func(c *Config) *float64 { return &c.TaskRecallMinScore },
		"taskRecallMinRelative":    func(c *Config) *float64 { return &c.TaskRecallMinRelative },
	}
	for key, field := range floats {
		for _, bad := range []float64{0, -0.1, 1.01, math.NaN()} {
			c := Default()
			*field(&c) = bad
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s %v accepted or unclear: %v", key, bad, err)
			}
		}
		c := Default()
		*field(&c) = 1
		if err := c.Validate(); err != nil {
			t.Errorf("%s 1: %v", key, err)
		}
	}
	for _, bad := range []int{0, -1, 1, 41} { // 1은 받지 않는다: 위임 글도 낱말 둘 이상이어야 회상한다(inject minRecallWords)
		c := Default()
		c.TaskRecallMinWords = bad
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "taskRecallMinWords") {
			t.Errorf("taskRecallMinWords %d accepted or unclear: %v", bad, err)
		}
	}
	// 제목 몫(1-6 b7f)은 0 < x ≤ 1이다.
	for _, bad := range []float64{0, -1, 1.01, math.NaN()} {
		c := Default()
		c.TaskRecallMinTitleShare = bad
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "taskRecallMinTitleShare") {
			t.Errorf("taskRecallMinTitleShare %v accepted or unclear: %v", bad, err)
		}
	}
	for _, ok := range []float64{0.5, 1} {
		c := Default()
		c.TaskRecallMinTitleShare = ok
		if err := c.Validate(); err != nil {
			t.Errorf("taskRecallMinTitleShare %v: %v", ok, err)
		}
	}
	if err := os.WriteFile(p, []byte(`{"recallMinCoverageOneWord":0.5,"taskRecallMinScore":0.2,"taskRecallMinWords":4,"taskRecallMinRelative":0.6,"taskRecallMinTitleShare":0.1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c.RecallMinCoverageOneWord != 0.5 || c.TaskRecallMinScore != 0.2 || c.TaskRecallMinWords != 4 || c.TaskRecallMinRelative != 0.6 ||
		c.TaskRecallMinTitleShare != 0.1 {
		t.Fatalf("file = %+v %v", c, err)
	}
}
