package host_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/host"
	"github.com/crispebble/orbit-mem/internal/inject"
	"github.com/crispebble/orbit-mem/internal/memory"
)

const (
	payTask    = "결제 모듈 동시성 시험을 고친다. 경합을 실제로 일으키는 시험을 쓴다"
	deployTask = "배포 스크립트의 롤백 절차를 문서로 정리한다"
)

type delegationWorld struct {
	e                *env
	s                *memory.Store
	core, pay, depl  memory.Memory
	delegateToolName string
}

// newDelegationWorld has a user core and two memories, each matching one of the two tasks.
func newDelegationWorld(t *testing.T) *delegationWorld {
	t.Helper()
	e := newEnv(t)
	s := e.store()
	w := &delegationWorld{e: e, s: s, delegateToolName: "Delegate"}
	w.core = pin(t, s, memory.SaveInput{Kind: memory.KindRule, Title: "답은 존댓말", Body: "반말 금지", Scope: memory.ScopeGlobal, Topic: "말투"})
	w.pay = save(t, s, memory.SaveInput{Kind: memory.KindRule, Scope: memory.ScopeGlobal, Topic: "결제 동시성 시험",
		Title: "결제 모듈 동시성 시험은 경합을 실제로 일으킨다", Body: "결제 모듈의 동시성 시험은 고루틴 여럿으로 경합을 실제로 일으킨다"})
	w.depl = save(t, s, memory.SaveInput{Kind: memory.KindRule, Scope: memory.ScopeGlobal, Topic: "배포 롤백",
		Title: "배포 스크립트 롤백 절차는 문서로 남긴다", Body: "배포 스크립트가 실패하면 롤백 절차 문서를 따른다"})
	save(t, s, memory.SaveInput{Kind: memory.KindRule, Scope: memory.ScopeGlobal, Topic: "도구 기억", Tools: []string{w.delegateToolName},
		Title: "맡길 때는 완료 기준을 함께 준다", Body: "위임 글에 완료 기준을 적는다"})
	return w
}

func (w *delegationWorld) delegate(session, kind, task string) host.Recall {
	return w.e.adapter(session).recall(host.OpToolContext, map[string]any{"tool": w.delegateToolName, "task": task, "agentKind": kind})
}

func (w *delegationWorld) start(session, agent, kind string, more map[string]any) host.Recall {
	a := w.e.adapter(session)
	a.agent = agent
	fields := map[string]any{"audience": "subagent", "agentKind": kind}
	for k, v := range more {
		fields[k] = v
	}
	return a.recall(host.OpContext, fields)
}

func roleOnly(t *testing.T, w *delegationWorld, what string, r host.Recall) {
	t.Helper()
	if !strings.HasPrefix(r.Text, inject.RoleHead) || !slices.Equal(r.MemoryIDs, []string{w.core.ID}) || r.Budget.Chars > 600 {
		t.Fatalf("%s: 핵심 제목 줄만이어야 한다(%d자) %v\n%s", what, r.Budget.Chars, r.MemoryIDs, r.Text)
	}
}

func recalled(t *testing.T, what string, r host.Recall, want, not memory.Memory) {
	t.Helper()
	if !slices.Contains(r.MemoryIDs, want.ID) || slices.Contains(r.MemoryIDs, not.ID) || r.Budget.Chars > inject.MaxSubagentRunes || r.Budget.Limit != inject.MaxSubagentRunes {
		t.Fatalf("%s: want %s not %s, got %v (%d/%d자)\n%s", what, want.ID, not.ID, r.MemoryIDs, r.Budget.Chars, r.Budget.Limit, r.Text)
	}
}

// 설계 4.1·11절: 위임 도구 직전(tool-context에 task)에 위임 글을 적고, 그 도구에 걸린 기억은 task가 없을 때와 같게 낸다.
// 바로 뒤 서브에이전트 시작(context audience=subagent)은 그 글로 위임 회상을 만들고, 글은 그 서브에이전트가 가져간다.
func TestDelegation_tool_context가_위임_글을_적고_서브에이전트_시작이_그_글로_회상한다(t *testing.T) {
	w := newDelegationWorld(t)
	with := w.delegate("s-1", "worker", payTask)
	without := w.e.adapter("s-2").recall(host.OpToolContext, map[string]any{"tool": w.delegateToolName})
	if with.Text == "" || with.Text != without.Text || !slices.Equal(with.MemoryIDs, without.MemoryIDs) {
		t.Fatalf("도구 기억이 달라졌다\nwith %q\nwithout %q", with.Text, without.Text)
	}
	recalled(t, "s-1 a-1", w.start("s-1", "a-1", "worker", nil), w.pay, w.depl)
	roleOnly(t, w, "s-1 a-2(남은 위임 없음)", w.start("s-1", "a-2", "worker", nil))
	roleOnly(t, w, "s-2(위임 없음)", w.start("s-2", "a-1", "worker", nil))
}

// 같은 세션에서 같은 종류를 둘 나란히 맡겨도 먼저 온 차례로 하나씩 짝이 맞고, 종류가 다르면 짝이 없다. 같은 서브에이전트가
// 다시 물으면 가져간 글을 쓴다.
func TestDelegation_같은_종류_둘을_나란히_맡겨도_먼저_온_차례로_하나씩_짝이_맞는다(t *testing.T) {
	w := newDelegationWorld(t)
	w.delegate("s-1", "worker", payTask)
	w.delegate("s-1", "worker", deployTask)
	roleOnly(t, w, "다른 종류", w.start("s-1", "r-1", "reviewer", nil))
	recalled(t, "첫째", w.start("s-1", "a-1", "worker", nil), w.pay, w.depl)
	recalled(t, "둘째", w.start("s-1", "a-2", "worker", nil), w.depl, w.pay)
	roleOnly(t, w, "셋째", w.start("s-1", "a-3", "worker", nil))
	recalled(t, "첫째 다시", w.start("s-1", "a-1", "worker", nil), w.pay, w.depl)
}

// task를 직접 준 context(bench·다른 호스트)는 그 글을 쓰고, 적어 둔 위임은 가져가지 않는다.
func TestDelegation_context에_task가_있으면_그_글을_쓰고_적힌_위임은_남긴다(t *testing.T) {
	w := newDelegationWorld(t)
	w.delegate("s-1", "worker", payTask)
	recalled(t, "task 직접", w.start("s-1", "a-1", "worker", map[string]any{"task": deployTask}), w.depl, w.pay)
	recalled(t, "적힌 위임", w.start("s-1", "a-2", "worker", nil), w.pay, w.depl)
}

// 위임 글은 reset(compact·clear)·세션 끝과 함께 지워진다.
func TestDelegation_reset과_session_end가_위임_글을_지운다(t *testing.T) {
	w := newDelegationWorld(t)
	w.delegate("s-1", "worker", payTask)
	if _, err := w.e.adapter("s-1").do(host.OpReset, nil); err != nil {
		t.Fatal(err)
	}
	roleOnly(t, w, "reset 뒤", w.start("s-1", "a-1", "worker", nil))
	w.delegate("s-1", "worker", payTask)
	if _, err := w.e.adapter("s-1").do(host.OpSessionEnd, nil); err != nil {
		t.Fatal(err)
	}
	w.e.svc.Wait()
	roleOnly(t, w, "세션 끝 뒤", w.start("s-1", "a-2", "worker", nil))
	var n int
	if err := w.s.DB().QueryRow(`SELECT COUNT(*) FROM session_bundles`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("남은 줄 %d %v", n, err)
	}
}

// 위임 글은 앞부분(2,000자)만 남고, 활동 기록·사건(지표·회고의 재료)·감사·activity_find 어디에도 새지 않는다.
func TestDelegation_위임_글은_앞부분만_남고_다른_기록에_새지_않는다(t *testing.T) {
	w := newDelegationWorld(t)
	const secret = "위임비밀낱말"
	long := secret + " " + payTask + strings.Repeat(" 덧붙인 설명", 600)
	w.delegate("s-1", "worker", long)
	var max int
	if err := w.s.DB().QueryRow(`SELECT MAX(length(text)) FROM session_bundles`).Scan(&max); err != nil || max != 2000 {
		t.Fatalf("남긴 글 %d자 %v", max, err)
	}
	a := w.e.adapter("s-1")
	a.recall(host.OpRecall, map[string]any{"prompt": "결제 모듈 동시성 시험을 어떻게 고치나"})
	recalled(t, "긴 위임", w.start("s-1", "a-1", "worker", nil), w.pay, w.depl)
	w.e.svc.Wait()
	out, herr := a.do(host.OpCall, map[string]any{"name": "activity_find", "args": map[string]any{}})
	if herr != nil {
		t.Fatal(herr)
	}
	if b, _ := json.Marshal(out); strings.Contains(string(b), secret) {
		t.Fatalf("activity_find에 샜다: %s", b)
	}
	tables, err := w.s.DB().Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT IN ('session_bundles') AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		_ = tables.Scan(&name)
		names = append(names, name)
	}
	tables.Close()
	if !slices.Contains(names, "events") || !slices.Contains(names, "activity") || !slices.Contains(names, "proposals") {
		t.Fatalf("tables %v", names)
	}
	for _, name := range names {
		rows, err := w.s.DB().Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			for i, v := range vals {
				var s string
				switch x := v.(type) {
				case string:
					s = x
				case []byte:
					s = string(x)
				}
				if strings.Contains(s, secret) {
					t.Errorf("%s.%s에 위임 글이 샜다", name, cols[i])
				}
			}
		}
		rows.Close()
	}
}

// 위임 글을 쓰는 곳은 위임 회상(recall auto)뿐이다: recall이 on_demand·off면 위임 도구 직전에도 위임 글을 남기지 않는다.
func TestDelegation_recall이_auto가_아니면_위임_글을_남기지_않는다(t *testing.T) {
	for _, mode := range []string{config.RecallOnDemand, config.RecallOff} {
		t.Run(mode, func(t *testing.T) {
			w := newDelegationWorld(t)
			w.e.cfg.Recall = mode
			w.delegate("s-1", "worker", payTask)
			var n int
			if err := w.s.DB().QueryRow(`SELECT COUNT(*) FROM session_bundles`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("recall %s: 위임 기록 %d행 %v", mode, n, err)
			}
		})
	}
}
