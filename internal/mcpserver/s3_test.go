package mcpserver

// 설계 · orbit-mem 2판 S3(명세 0s4fsq3hy8pnp): 적용 대상 기본값(D3, S3-3), scope 별칭 한 판(S3-4), 승격과 한도 거절(S3-5),
// memory_find가 회상과 같은 판정으로 제품 범위를 보인다(S3-1).

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit-mem/internal/memory"
	"github.com/onidot-labs/orbit-mem/internal/place"
)

func s3Map() place.Map {
	return place.Map{
		"orbit-code-desktop": {Match: []string{"github.com/onidot-labs/orbit-studio", "/w/onidot/prototypes/orbit-desktop-*", "/w/onidot"}},
		"atlas":              {Match: []string{"github.com/onidot-labs/atlas", "/w/onidot"}},
	}
}

// connectAt is a host session working in the folder of scope, with the S3 product map, over store s.
func connectAt(t *testing.T, s *memory.Store, scope string) *mcp.ClientSession {
	t.Helper()
	label := strings.TrimPrefix(scope, "project:")
	return connectServer(t, New(s, Options{Project: scope, ProjectLabel: label, Version: "test", Products: s3Map()}))
}

func s3Store(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

const (
	deskRepo = "project:github.com/onidot-labs/orbit-studio"
	parent   = "project:/w/onidot"
	draft    = "project:/w/onidot/prototypes/orbit-desktop-v4-2026-11-03"
	atlasDir = "project:github.com/onidot-labs/atlas"
	other    = "project:github.com/someone/unrelated"
)

func TestSave_적용_대상_기본값은_지금_폴더의_제품이고_모르면_product_물음표(t *testing.T) {
	s := s3Store(t)
	cases := []struct {
		where, applies, warn string
	}{
		{deskRepo, "product:orbit-code-desktop", ""},
		{draft, "product:orbit-code-desktop", ""},
		{parent, "product:?", "appliesTo"},
		{other, "product:?", "appliesTo"},
	}
	for i, c := range cases {
		var out MemoryOut
		res := call(t, connectAt(t, s, c.where), "memory_save", map[string]any{"kind": "fact", "title": "사실 " + string(rune('가'+i)), "source": quote("x")}, &out)
		if res.IsError || out.Memory.AppliesTo != c.applies {
			t.Fatalf("%s: %v %s %+v", c.where, res.IsError, text(res), out.Memory)
		}
		if c.warn == "" && len(out.Warnings) > 0 || c.warn != "" && !strings.Contains(strings.Join(out.Warnings, " "), c.warn) {
			t.Fatalf("%s warnings %v", c.where, out.Warnings)
		}
		if c.applies == "product:?" && out.Memory.Project != strings.TrimPrefix(c.where, "project:") {
			t.Fatalf("%s: product:? keeps the folder, got %q", c.where, out.Memory.Project)
		}
	}
	// user는 모든 일에 적용된다는 경고와 지금 user 핵심 길이를 싣는다.
	var out MemoryOut
	call(t, connectAt(t, s, deskRepo), "memory_save", map[string]any{"kind": "rule", "title": "답은 존댓말", "topic": "말투", "appliesTo": "user", "source": quote("존댓말")}, &out)
	if out.Memory.AppliesTo != "user" || out.Memory.Scope != memory.ScopeGlobal || !strings.Contains(strings.Join(out.Warnings, " "), "3,000") {
		t.Fatalf("user: %+v %v", out.Memory, out.Warnings)
	}
	// 명시한 값: 이 폴더의 제품·저장소, 다른 제품, 지도에 없는 제품(경고).
	for applies, want := range map[string]string{
		"product": "product:orbit-code-desktop", "repo": "repo:github.com/onidot-labs/orbit-studio", "product:atlas": "product:atlas", "product:없는제품": "",
	} {
		var o MemoryOut
		res := call(t, connectAt(t, s, deskRepo), "memory_save", map[string]any{"kind": "fact", "title": "명시 " + applies, "appliesTo": applies, "source": quote("x")}, &o)
		if want == "" {
			if !res.IsError {
				t.Fatalf("bad name accepted: %+v", o.Memory)
			}
			continue
		}
		if res.IsError || o.Memory.AppliesTo != want {
			t.Fatalf("%s: %s %+v", applies, text(res), o.Memory)
		}
	}
	var o MemoryOut
	call(t, connectAt(t, s, deskRepo), "memory_save", map[string]any{"kind": "fact", "title": "지도 밖 제품", "appliesTo": "product:doraft", "source": quote("x")}, &o)
	if o.Memory.AppliesTo != "product:doraft" || !strings.Contains(strings.Join(o.Warnings, " "), "제품 지도") {
		t.Fatalf("unmapped product: %+v %v", o.Memory, o.Warnings)
	}
}

func TestSave_scope_별칭은_한_판_동안_받고_경고한다(t *testing.T) {
	s := s3Store(t)
	cs := connectAt(t, s, deskRepo)
	var g, p MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "옛 전역", "scope": "global", "source": quote("x")}, &g)
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "옛 프로젝트", "scope": "project", "source": quote("x")}, &p)
	if g.Memory.AppliesTo != "user" || p.Memory.AppliesTo != "repo:github.com/onidot-labs/orbit-studio" {
		t.Fatalf("alias: %s %s", g.Memory.AppliesTo, p.Memory.AppliesTo)
	}
	for _, o := range []MemoryOut{g, p} {
		if !strings.Contains(strings.Join(o.Warnings, " "), "appliesTo") {
			t.Fatalf("alias warning %v", o.Warnings)
		}
	}
	if res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "어긋남", "scope": "global", "appliesTo": "product", "source": quote("x")}, nil); !res.IsError {
		t.Fatal("conflicting scope and appliesTo accepted")
	}
	if res := call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "같음", "scope": "global", "appliesTo": "user", "source": quote("x")}, nil); res.IsError {
		t.Fatalf("matching scope and appliesTo refused: %s", text(res))
	}
	// memory_update도 옛 scope로 옮길 수 있고, 새 appliesTo로도 옮긴다.
	var u MemoryOut
	// 사용자 말을 담은 기억이라 옮기려면 사용자 말(userQuote)이 필요하다(A2).
	call(t, cs, "memory_update", map[string]any{"id": p.Memory.ID, "scope": "global", "userQuote": "전역으로 옮겨"}, &u)
	if u.Memory.AppliesTo != "user" {
		t.Fatalf("update alias: %+v", u.Memory)
	}
	call(t, cs, "memory_update", map[string]any{"id": p.Memory.ID, "appliesTo": "product:atlas", "userQuote": "atlas 것"}, &u)
	if u.Memory.AppliesTo != "product:atlas" {
		t.Fatalf("update appliesTo: %+v", u.Memory)
	}
}

func TestFind_here는_회상과_같은_판정으로_제품_범위를_보인다(t *testing.T) {
	s := s3Store(t)
	call(t, connectAt(t, s, deskRepo), "memory_save", map[string]any{"kind": "fact", "title": "레일 폭은 48", "source": quote("x")}, nil)
	for where, want := range map[string]bool{deskRepo: true, parent: true, draft: true, atlasDir: false, other: false} {
		var out FindOut
		call(t, connectAt(t, s, where), "memory_find", map[string]any{"query": "레일 폭"}, &out)
		if (len(out.Items) == 1) != want {
			t.Fatalf("%s: %+v", where, out.Items)
		}
		if want && out.Items[0].AppliesTo != "product:orbit-code-desktop" {
			t.Fatalf("summary appliesTo %+v", out.Items[0])
		}
	}
}

func TestPin_승격은_한도를_넘으면_거절하고_핵심_목록을_보인다(t *testing.T) {
	s := s3Store(t)
	cs := connectAt(t, s, deskRepo)
	var big MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "큰 핵심", "topic": "큰", "appliesTo": "user", "body": strings.Repeat("가", 990),
		"pin": true, "source": quote("늘 지켜")}, &big)
	if !big.Memory.Pinned {
		t.Fatalf("pin on save: %+v", big.Memory)
	}
	var second MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "둘째 핵심", "topic": "둘째", "appliesTo": "user", "body": strings.Repeat("나", 990), "source": quote("x")}, &second)
	// 사용자 말을 담은 기억이라 AI는 사용자 말(userQuote)과 함께 올린다(A2).
	if res := call(t, cs, "memory_update", map[string]any{"id": second.Memory.ID, "action": "pin"}, nil); !res.IsError {
		t.Fatal("AI pinned the user's words without userQuote")
	}
	if res := call(t, cs, "memory_update", map[string]any{"id": second.Memory.ID, "action": "pin", "userQuote": "늘 지켜"}, nil); res.IsError {
		t.Fatalf("pin: %s", text(res))
	}
	var third MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "셋째 핵심", "topic": "셋째", "appliesTo": "user", "body": strings.Repeat("다", 990), "source": quote("x")}, &third)
	res := call(t, cs, "memory_update", map[string]any{"id": third.Memory.ID, "action": "pin", "userQuote": "늘 지켜"}, nil)
	if !res.IsError || !strings.Contains(text(res), big.Memory.ID) || !strings.Contains(text(res), second.Memory.ID) {
		t.Fatalf("over limit: %v %s", res.IsError, text(res))
	}
	n := len(mustList(t, s))
	res = call(t, cs, "memory_save", map[string]any{"kind": "rule", "title": "넷째", "topic": "넷째", "appliesTo": "user", "body": strings.Repeat("라", 1000), "pin": true, "source": quote("x")}, nil)
	if !res.IsError || len(mustList(t, s)) != n {
		t.Fatalf("save pin over limit: %v %s", res.IsError, text(res))
	}
	if res := call(t, cs, "memory_update", map[string]any{"id": big.Memory.ID, "action": "unpin", "userQuote": "그건 내려"}, nil); res.IsError {
		t.Fatal(text(res))
	}
	if res := call(t, cs, "memory_update", map[string]any{"id": third.Memory.ID, "action": "pin", "userQuote": "늘 지켜"}, nil); res.IsError {
		t.Fatalf("pin after unpin: %s", text(res))
	}
	// 핵심 기억을 승격할 수 없는 자리(이 저장소)로 옮기면 핵심에서 빠졌다고 알린다.
	var moved MemoryOut
	call(t, cs, "memory_update", map[string]any{"id": third.Memory.ID, "appliesTo": "repo", "userQuote": "이 저장소 것"}, &moved)
	if moved.Memory.Pinned || !strings.Contains(strings.Join(moved.Warnings, " "), "상시 핵심에서 빠짐") {
		t.Fatalf("moved out of the core: %v %v", moved.Memory.Pinned, moved.Warnings)
	}
	// 제품 미정·저장소 기억은 승격할 수 없다.
	var unsure MemoryOut
	call(t, connectAt(t, s, other), "memory_save", map[string]any{"kind": "fact", "title": "미정", "source": quote("x")}, &unsure)
	if res := call(t, cs, "memory_update", map[string]any{"id": unsure.Memory.ID, "action": "pin"}, nil); !res.IsError {
		t.Fatal("product:? pinned")
	}
}

func mustList(t *testing.T, s *memory.Store) []memory.Memory {
	t.Helper()
	ms, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

var _ = context.Background

// 검토 지적(2026-11-04): 오래 사는 서버도 호출마다 제품 지도를 다시 읽고, 읽지 못하면 경고한다.
func TestSave_서버는_호출마다_제품_지도를_다시_읽는다(t *testing.T) {
	s := s3Store(t)
	var m place.Map
	var loadErr error
	cs := connectServer(t, New(s, Options{Project: draft, ProjectLabel: strings.TrimPrefix(draft, "project:"), Version: "test",
		LoadProducts: func() (place.Map, error) { return m, loadErr }}))
	var before MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "지도 전", "source": quote("x")}, &before)
	m = s3Map()
	var after MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "지도 뒤", "source": quote("x")}, &after)
	if before.Memory.AppliesTo != "product:?" || after.Memory.AppliesTo != "product:orbit-code-desktop" {
		t.Fatalf("before %s after %s", before.Memory.AppliesTo, after.Memory.AppliesTo)
	}
	m, loadErr = nil, errors.New("config.json: bad")
	var broken MemoryOut
	call(t, cs, "memory_save", map[string]any{"kind": "fact", "title": "지도 깨짐", "source": quote("x")}, &broken)
	if !strings.Contains(strings.Join(broken.Warnings, " "), "제품 지도") {
		t.Fatalf("no warning for a broken map: %v", broken.Warnings)
	}
}
