package inject

// 「왜 안 나왔나」 진단(기획 · Orbit Code K15). 기억 하나가 요청에
// 들어가지 않은 까닭을 여섯 단계로 나눠 각 단계를 따로 판정한다: 저장됨 → 적격 → 선택됨 → 주입됨 → 전달됨 → 사용됨.
// 한 단계의 통과가 다음 단계를 증명하지 않는다. 읽기만 하고 아무것도 고치지 않으며 사건도 남기지 않는다.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// Stage names, in order.
const (
	StageStored    = "stored"
	StageEligible  = "eligible"
	StageSelected  = "selected"
	StageInjected  = "injected"
	StageDelivered = "delivered"
	StageUsed      = "used"
)

// Stage verdicts.
const (
	Yes     = "yes"
	No      = "no"
	Unknown = "unknown" // 증거가 없어 판정하지 못함
	Skipped = "skipped" // 앞 단계에서 멈춰 보지 않음
)

// Stage is one claim of the chain, its verdict and why (a code a person's
// screen renders, with its arguments).
type Stage struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Code   string   `json:"code"`
	Args   []string `json:"args,omitempty"`
}

// Note is something worth knowing that does not stop the chain.
type Note struct {
	Code string   `json:"code"`
	Args []string `json:"args,omitempty"`
}

// Diagnosis is the answer to "why was this memory not used?".
type Diagnosis struct {
	MemoryID   string  `json:"memoryId"`
	Title      string  `json:"title,omitempty"`
	Path       string  `json:"path"` // recall(요청 회상) · session_rules(세션 시작 확정 규칙)
	Stages     []Stage `json:"stages"`
	Notes      []Note  `json:"notes,omitempty"`
	LastProven string  `json:"lastProven"` // 증거로 통과한 마지막 단계(none이면 없음)
	StoppedAt  string  `json:"stoppedAt,omitempty"`
}

// DiagnoseInput says which memory and which request and session to look at.
type DiagnoseInput struct {
	ID, Scope, Prompt, Host, Session string
	Options                          Options
}

// Diagnose walks one memory through the six stages for a request (prompt)
// and, when given, a host session. Without a prompt the selection is unknown;
// without a session delivery and use are unknown.
// hookHosts are the hosts that take the hook context into the request as it is (Claude Code, Codex).
// Any other host calls orbit-mem inject and reports what it put in with inject delivered.
var hookHosts = []string{"claude", "codex"}

func Diagnose(s *memory.Store, cfg config.Config, in DiagnoseInput) (d Diagnosis) {
	d = Diagnosis{MemoryID: in.ID, Path: "recall"}
	add := func(name, status, code string, args ...string) {
		d.Stages = append(d.Stages, Stage{Name: name, Status: status, Code: code, Args: args})
	}
	defer d.finish()
	m, err := s.Get(in.ID)
	switch {
	case err != nil:
		add(StageStored, No, "not_found")
		return d
	case m.State == memory.StateHidden:
		d.Title = m.Title
		add(StageStored, No, "hidden")
		return d
	case m.State == memory.StateStale:
		d.Title = m.Title
		add(StageStored, No, "stale", m.StaleReason, m.SupersededBy)
		return d
	}
	d.Title = m.Title
	add(StageStored, Yes, "stored", m.State, m.Kind)

	bundle := SessionStartInjectionWith(s, cfg, in.Scope, in.Options).MemoryIDs
	if in.Session != "" {
		if _, ids, ok := s.SessionBundle(in.Host, in.Session); ok {
			bundle = ids
		}
	}
	_, why, _ := CandidatesAt(s, at(cfg, in.Scope), in.Options, bundle)
	now := s.Now()
	// 세션 묶음에 든 상시 핵심과 확정 규칙(inject=rules 판, 분야 규칙 빼고)은 세션 시작으로 들어간다(S3-R6).
	// 묶음에 없는 규칙은 요청 회상 길로 본다.
	sessionRule := m.State == memory.StateConfirmed && slices.Contains(bundle, m.ID) &&
		(m.Pinned || m.Kind == memory.KindRule && memory.DomainOf(m) == "")
	switch reason := why[m.ID]; {
	case sessionRule && (reason == "" || reason == "session_rule"):
		d.Path = "session_rules"
		add(StageEligible, Yes, "session_rule")
	case reason == "":
		add(StageEligible, Yes, "eligible")
	case reason == "scope":
		add(StageEligible, No, "out_of_scope", m.Scope, in.Scope)
	case reason == "journal":
		add(StageEligible, No, "journal")
	case reason == ExcludedExpired:
		add(StageEligible, No, "expired", day(m.ExpiresAt))
	case reason == ExcludedSuperseded:
		heads, _ := s.TopicHeads()
		add(StageEligible, No, "superseded", heads.Superseding(m), m.Topic)
	default:
		add(StageEligible, No, why[m.ID])
	}
	if m.Freshness(now) == memory.FreshReviewDue {
		d.Notes = append(d.Notes, Note{Code: "review_due", Args: []string{day(m.ReviewAt)}})
	}
	for _, src := range m.Sources {
		if src.Kind == memory.SourceFile && src.Hash != "" && memory.FileHash(src.Ref) != src.Hash {
			d.Notes = append(d.Notes, Note{Code: "source_changed", Args: []string{src.Ref}})
		}
	}
	if d.Stages[len(d.Stages)-1].Status != Yes {
		return d
	}

	observedKind := memory.EventInjectPrompt
	if d.Path == "session_rules" {
		observedKind = memory.EventInjectSessionStart
		inj := SessionStartInjectionWith(s, cfg, in.Scope, in.Options)
		if slices.Contains(inj.MemoryIDs, m.ID) {
			add(StageSelected, Yes, "in_bundle")
		} else {
			add(StageSelected, No, "not_in_bundle")
		}
	} else if strings.TrimSpace(in.Prompt) == "" {
		add(StageSelected, Unknown, "no_prompt")
	} else if hits := memory.Strong([]memory.Doc{m.Doc()}, in.Prompt); len(hits) == 0 {
		matched, terms := matchedTerms(m.Doc(), in.Prompt)
		add(StageSelected, No, "weak_match", strings.Join(matched, " "), strings.Join(terms, " "))
	} else {
		add(StageSelected, Yes, "matched", fmt.Sprintf("%.1f", hits[0].Score))
	}

	// 주입됨: 이 세션에 넣은 기록이 있으면 그것이 증거다. 없으면 지금 같은 요청으로 묶음을 다시 만들어 본다(넣지 않음).
	var observed []sessionEvent
	if in.Session != "" {
		observed = sessionEvents(s, in.Session, in.Host, m.ID, observedKind)
	}
	selected := d.Stages[len(d.Stages)-1].Status
	switch {
	case len(observed) > 0:
		add(StageInjected, Yes, "observed", stamp(observed[len(observed)-1].at))
	case selected == Yes && d.Path == "session_rules":
		add(StageInjected, Unknown, "would_inject_rules")
	case selected == Yes:
		inj := PromptInjectionWith(s, cfg, in.Scope, in.Prompt, in.Options)
		if i := slices.Index(inj.MemoryIDs, m.ID); i >= 0 {
			add(StageInjected, Unknown, "would_inject", fmt.Sprint(i+1))
		} else {
			budget := cfg.RecallChars
			if budget <= 0 {
				budget = config.Default().RecallChars
			}
			add(StageInjected, No, "over_budget", fmt.Sprint(budget))
		}
	case selected == Unknown:
		add(StageInjected, Unknown, "no_prompt")
	default:
		add(StageInjected, Skipped, "skipped")
	}

	if in.Session == "" {
		add(StageDelivered, Unknown, "no_session")
		add(StageUsed, Unknown, "no_session")
		return d
	}
	if delivered := sessionEvents(s, in.Session, in.Host, m.ID, memory.EventInjectDelivered); len(delivered) > 0 {
		add(StageDelivered, Yes, "delivered", stamp(delivered[len(delivered)-1].at))
	} else if len(observed) > 0 && slices.Contains(hookHosts, observed[0].host) {
		// Claude Code·Codex 훅은 돌려준 문맥을 호스트가 그대로 요청에 넣는다. 다른 호스트(orbit-mem inject를
		// 부르는 쪽)는 넣었음을 inject delivered로 알려야 한다(기획 · orbit-mem M-4).
		add(StageDelivered, Yes, "hook_returned", observed[0].host)
	} else if len(observed) > 0 {
		add(StageDelivered, No, "not_delivered")
	} else {
		add(StageDelivered, Unknown, "no_record")
	}

	used := sessionEvents(s, in.Session, "", m.ID, memory.EventMemoryUsed)
	opened := sessionEvents(s, in.Session, in.Host, m.ID, memory.EventToolPrefix+"memory_get")
	switch {
	case len(used) > 0:
		var v struct {
			Verdict string `json:"verdict"`
		}
		_ = json.Unmarshal([]byte(used[len(used)-1].data), &v)
		status := No
		if v.Verdict == memory.UsageFollowed {
			status = Yes
		}
		add(StageUsed, status, "verdict_"+v.Verdict)
	case len(opened) > 0:
		add(StageUsed, Yes, "opened", stamp(opened[len(opened)-1].at))
	default:
		add(StageUsed, Unknown, "no_evidence")
	}
	return d
}

func (d *Diagnosis) finish() {
	d.LastProven = "none"
	for _, st := range d.Stages {
		if st.Status == Yes {
			d.LastProven = st.Name
		}
		if st.Status == No && d.StoppedAt == "" {
			d.StoppedAt = st.Name
		}
	}
}

type sessionEvent struct {
	at   int64
	host string
	data string
}

// sessionEvents are the events of kind in a session (of a host, if given) that name memory id.
func sessionEvents(s *memory.Store, session, host, id, kind string) []sessionEvent {
	q := `SELECT at, host, data FROM events WHERE session=? AND kind=? AND data LIKE ?`
	args := []any{session, kind, `%"` + id + `"%`}
	if host != "" {
		q += ` AND host=?`
		args = append(args, host)
	}
	rows, err := s.DB().Query(q+` ORDER BY id`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []sessionEvent
	for rows.Next() {
		var e sessionEvent
		var h sql.NullString
		if rows.Scan(&e.at, &h, &e.data) == nil {
			e.host = h.String
			out = append(out, e)
		}
	}
	return out
}

// matchedTerms are the prompt words that appear in d, and all the words the request recall looks at.
func matchedTerms(d memory.Doc, prompt string) (matched, terms []string) {
	for _, t := range memory.PromptTerms(prompt) {
		terms = append(terms, t)
		if memory.Relevance(d, t).Score > 0 {
			matched = append(matched, t)
		}
	}
	return matched, terms
}

func day(ms int64) string { return time.UnixMilli(ms).Local().Format("2006-01-02") }

func stamp(ms int64) string { return time.UnixMilli(ms).Local().Format("2006-01-02 15:04") }
