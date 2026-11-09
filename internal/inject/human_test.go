package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type humanFixtures struct {
	Persons []struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
		Person bool   `json:"person"`
		Words  string `json:"words"`
	} `json:"persons"`
	Requests []struct {
		Name    string `json:"name"`
		Words   string `json:"words"`
		Text    string `json:"text"`
		Trivial bool   `json:"trivial"`
	} `json:"requests"`
}

func loadHumanFixtures(t *testing.T) humanFixtures {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "human_fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx humanFixtures
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestPersonWords_고정_묶음(t *testing.T) {
	fx := loadHumanFixtures(t)
	if len(fx.Persons) < 20 {
		t.Fatalf("사례가 너무 적다: %d", len(fx.Persons))
	}
	for _, c := range fx.Persons {
		t.Run(c.Name, func(t *testing.T) {
			words, ok := PersonWords(c.Prompt)
			if ok != c.Person || (c.Person && words != c.Words) {
				t.Fatalf("PersonWords(%q) = (%q, %v), want (%q, %v)", c.Prompt, words, ok, c.Words, c.Person)
			}
		})
	}
}

func TestRequestText_고정_묶음(t *testing.T) {
	fx := loadHumanFixtures(t)
	if len(fx.Requests) < 20 {
		t.Fatalf("사례가 너무 적다: %d", len(fx.Requests))
	}
	for _, c := range fx.Requests {
		t.Run(c.Name, func(t *testing.T) {
			text, trivial := RequestText(c.Words)
			if text != c.Text || trivial != c.Trivial {
				t.Fatalf("RequestText(%q) = (%q, %v), want (%q, %v)", c.Words, text, trivial, c.Text, c.Trivial)
			}
		})
	}
}
