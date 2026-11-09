package main

import (
	"encoding/json"
	"testing"

	"github.com/crispebble/orbit-mem/internal/mcpserver"
	"github.com/crispebble/orbit-mem/internal/memory"
)

func savedTrust(t *testing.T, out string) string {
	t.Helper()
	var saved struct {
		Memory memory.Memory `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &saved); err != nil || saved.Memory.ID == "" {
		t.Fatalf("save out %q: %v", out, err)
	}
	s, err := openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := s.Get(saved.Memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m.Trust
}

// 통로 상한: host call·tool 별칭은 agent. ORBIT_ACTOR=user나 --actor user, 요청의 owner로 올릴 수 없다.
func TestTool_저장의_출처_등급은_통로_상한_agent다(t *testing.T) {
	home(t)
	seed(t).Close()
	base := []string{"--cwd", "/tmp", "--host", "h", "--session", "s"}
	for i, c := range []struct {
		env, flag, args, want string
	}{
		{"", "", `{"kind":"fact","appliesTo":"user","title":"기본","source":{"kind":"session","ref":"s"}}`, memory.TrustAgent},
		{"user", "", `{"kind":"fact","appliesTo":"user","title":"환경 user","source":{"kind":"session","ref":"s"}}`, memory.TrustAgent},
		{"", "user", `{"kind":"fact","appliesTo":"user","title":"플래그 user","source":{"kind":"session","ref":"s"}}`, memory.TrustAgent},
		{"user", "user", `{"kind":"fact","appliesTo":"user","title":"요청 owner","source":{"kind":"session","ref":"s","trust":"owner"}}`, memory.TrustAgent},
		{"", "", `{"kind":"fact","appliesTo":"user","title":"웹 글","source":{"kind":"file","ref":"web","trust":"untrusted"}}`, memory.TrustUntrusted},
	} {
		t.Setenv("ORBIT_ACTOR", c.env)
		args := append([]string{"tool", "memory_save"}, base...)
		if c.flag != "" {
			args = append(args, "--actor", c.flag)
		}
		out, errOut, code := run(t, c.args, args...)
		if code != 0 {
			t.Fatalf("#%d code %d out %q err %q", i, code, out, errOut)
		}
		if got := savedTrust(t, out); got != c.want {
			t.Errorf("#%d 등급 = %q, want %q", i, got, c.want)
		}
	}
}

// host call의 공통 칸 trust는 이번 호출의 상한을 낮춘다. 올리지는 못한다.
func TestHostCall_공통_칸_trust로_낮춘_저장(t *testing.T) {
	home(t)
	seed(t).Close()
	for _, c := range []struct{ trust, want string }{{`"untrusted"`, memory.TrustUntrusted}, {`"owner"`, memory.TrustAgent}, {`"system"`, memory.TrustSystem}} {
		in := `{"host":"h","session":"s","cwd":"/tmp","trust":` + c.trust + `,"name":"memory_save","args":{"kind":"fact","appliesTo":"user","title":"호출 ` + c.want + ` ` + c.trust[1:len(c.trust)-1] + `","source":{"kind":"session","ref":"s"}}}`
		out, errOut, code := run(t, in, "host", "call")
		if code != 0 {
			t.Fatalf("%s: code %d out %q err %q", c.trust, code, out, errOut)
		}
		var env struct {
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal([]byte(out), &env)
		if got := savedTrust(t, string(env.Result)); got != c.want {
			t.Errorf("trust %s = %q, want %q", c.trust, got, c.want)
		}
	}
	_, _, code := run(t, `{"host":"h","session":"s","cwd":"/tmp","trust":{"level":"x"},"name":"memory_find","args":{}}`, "host", "call")
	if code != 2 {
		t.Errorf("잘못된 trust 꼴 종료 코드 = %d, want 2", code)
	}
}

// 사람 CLI(thin CLI)의 도구는 상한 owner, MCP·host call의 상한은 agent다.
func TestTools_사람_통로의_상한은_owner다(t *testing.T) {
	home(t)
	seed(t).Close()
	tl, s, err := tools()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := tl.Save(mcpserver.SaveIn{Kind: "fact", Title: "사람이 남김", Body: "본문", AppliesTo: "user", Source: mcpserver.SourceIn{Kind: "session", Ref: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Get(out.Memory.ID); m.Trust != memory.TrustOwner {
		t.Errorf("사람 통로 등급 = %q", m.Trust)
	}
	if humanTrustLimit != memory.TrustOwner || mcpTrustLimit != memory.TrustAgent || hostTrustLimit != memory.TrustAgent {
		t.Errorf("상한 = %q %q %q", humanTrustLimit, mcpTrustLimit, hostTrustLimit)
	}
}
