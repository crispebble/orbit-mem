package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// recallFixtures is the fixed recall-quality set (testdata/recall_fixtures.json).
type recallFixtures struct {
	Scope    string `json:"scope"`
	Memories []struct {
		Key             string   `json:"key"`
		Kind            string   `json:"kind"`
		Scope           string   `json:"scope"`
		Quote           bool     `json:"quote"`
		Title           string   `json:"title"`
		Body            string   `json:"body"`
		Tags            []string `json:"tags"`
		Retention       string   `json:"retention"`
		TTLDays         int      `json:"ttlDays"`
		ReviewAfterDays int      `json:"reviewAfterDays"`
		Topic           string   `json:"topic"`
		AgeDays         int      `json:"ageDays"`
		Followed        int      `json:"followed"` // 세션 끝 회고가 「따름」으로 판정한 횟수(최근)
	} `json:"memories"`
	Cases []struct {
		Name     string   `json:"name"`
		Prompt   string   `json:"prompt"`
		Want     []string `json:"want"`
		Absent   []string `json:"absent"`
		Contains []string `json:"contains"`
	} `json:"cases"`
}

// TestRecallFixtures runs the fixed set through the work recall with no model:
// a change to recall that moves any answer shows up here.
func TestRecallFixtures(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "recall_fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx recallFixtures
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.Local)
	ids := map[string]string{}
	for _, m := range fx.Memories {
		at := now.AddDate(0, 0, -m.AgeDays)
		s.Now = func() time.Time { return at }
		scope := m.Scope
		if scope == "here" {
			scope = fx.Scope
		}
		src := memory.Source{Kind: memory.SourceSession, Ref: "fixture"}
		if m.Quote {
			src = memory.Source{Kind: memory.SourceQuote, Excerpt: m.Title}
		}
		saved, err := s.Save(memory.SaveInput{Kind: m.Kind, Title: m.Title, Body: m.Body, Tags: m.Tags, Scope: scope, Source: src,
			Retention: m.Retention, TTLDays: m.TTLDays, ReviewAfterDays: m.ReviewAfterDays, Topic: m.Topic})
		if err != nil {
			t.Fatalf("%s: %v", m.Key, err)
		}
		ids[m.Key] = saved.ID
		for i := 0; i < m.Followed; i++ {
			if err := s.RecordEvent(memory.Event{Kind: memory.EventMemoryUsed, Day: memory.Day(now)}, map[string]any{"memoryId": saved.ID, "verdict": memory.UsageFollowed}); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.Now = func() time.Time { return now }
	keyOf := map[string]string{}
	for k, id := range ids {
		keyOf[id] = k
	}
	for _, c := range fx.Cases {
		t.Run(c.Name, func(t *testing.T) {
			inj := PromptInjectionWith(s, config.Default(), fx.Scope, c.Prompt, Options{})
			var got []string
			for _, id := range inj.MemoryIDs {
				got = append(got, keyOf[id])
			}
			if len(got) < len(c.Want) || !slices.Equal(got[:len(c.Want)], c.Want) || (len(c.Want) == 0 && len(got) != 0 && len(c.Absent) == 0) {
				t.Errorf("got %v, want first %v\n%s", got, c.Want, inj.Text)
			}
			for _, k := range c.Absent {
				if slices.Contains(got, k) {
					t.Errorf("%s must not be recalled: %v", k, got)
				}
			}
			for _, w := range c.Contains {
				if !strings.Contains(inj.Text, w) {
					t.Errorf("text lacks %q:\n%s", w, inj.Text)
				}
			}
		})
	}
}
