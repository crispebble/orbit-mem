package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onidot-labs/orbit/internal/config"
	"github.com/onidot-labs/orbit/internal/inject"
	"github.com/onidot-labs/orbit/internal/mcpserver"
	"github.com/onidot-labs/orbit/internal/memory"
	"github.com/onidot-labs/orbit/internal/project"
	"github.com/onidot-labs/orbit/internal/version"
)

func paths() config.Paths { return config.Paths{Home: config.Home()} }

func loadConfig() (config.Config, error) { return config.Load(paths().Config()) }

func openStore() (*memory.Store, error) { return memory.Open(paths().DB()) }

func cmdVersion(a *app, args []string) error {
	fmt.Fprintln(a.stdout, "oni "+version.Version)
	return nil
}

func sessionProject() project.Project {
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return project.Resolve(dir)
}

func cmdMCP(a *app, args []string) error {
	fs := a.flags("mcp")
	retroID := fs.String("retro", "", "회고 id(회고 실행 전용 도구만 연다)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := signalContext()
	defer cancel()
	var srv *mcp.Server
	if *retroID != "" {
		srv = mcpserver.NewRetro(s, paths(), *retroID, version.Version)
	} else {
		p := sessionProject()
		srv = mcpserver.New(s, mcpserver.Options{Project: p.Scope, ProjectLabel: p.Label, Version: version.Version})
	}
	return srv.Run(ctx, &mcp.StdioTransport{})
}

type hookInput struct {
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
}

// cmdHook never fails the host: any problem prints nothing and exits 0.
func cmdHook(a *app, args []string) error {
	fs := a.flags("hook")
	host := fs.String("host", "claude", "claude | codex")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 || (pos[0] != "session-start" && pos[0] != "prompt") {
		return errUsage
	}
	_ = host
	done := make(chan string, 1)
	go func() {
		defer func() { recover() }()
		var in hookInput
		b, _ := io.ReadAll(io.LimitReader(a.stdin, 4<<20))
		_ = json.Unmarshal(b, &in)
		cfg, err := loadConfig()
		if err != nil {
			fmt.Fprintln(a.stderr, "oni hook: 설정을 읽지 못해 기억을 넣지 않았습니다: "+err.Error())
			done <- ""
			return
		}
		if _, err := os.Stat(paths().DB()); err != nil {
			done <- ""
			return
		}
		s, err := openStore()
		if err != nil {
			done <- ""
			return
		}
		defer s.Close()
		scope := ""
		if in.Cwd != "" {
			scope = project.Resolve(in.Cwd).Scope
		}
		if pos[0] == "session-start" {
			done <- inject.HookOutput("SessionStart", inject.SessionStart(s, cfg, scope))
		} else {
			done <- inject.HookOutput("UserPromptSubmit", inject.Prompt(s, cfg, scope, in.Prompt))
		}
	}()
	select {
	case out := <-done:
		if out != "" {
			fmt.Fprintln(a.stdout, out)
		}
	case <-time.After(4 * time.Second):
		fmt.Fprintln(a.stderr, "oni hook: 시간이 걸려 기억을 넣지 않았습니다")
	}
	return nil
}

func kindName(k string) string {
	return map[string]string{"rule": "규칙", "fact": "사실", "decision": "결정", "lesson": "교훈", "journal": "일지"}[k]
}

func stateName(s string) string {
	return map[string]string{"candidate": "후보", "confirmed": "확정", "stale": "낡음", "hidden": "숨김"}[s]
}

func cmdMemory(a *app, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	switch args[0] {
	case "find":
		fs := a.flags("memory find")
		var kinds multiFlag
		fs.Var(&kinds, "kind", "종류(여러 번)")
		all := fs.Bool("all", false, "모든 프로젝트")
		stale := fs.Bool("stale", false, "낡은 기억도")
		limit := fs.Int("n", 20, "최대 개수")
		asJSON := fs.Bool("json", false, "JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		in := memory.FindInput{Query: strings.Join(pos, " "), Kinds: kinds, Limit: *limit, AllProjects: *all, Project: sessionProject().Scope}
		if *stale {
			in.States = []string{memory.StateCandidate, memory.StateConfirmed, memory.StateStale}
		}
		ms, err := s.Find(in)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ms)
		}
		for _, m := range ms {
			fmt.Fprintf(a.stdout, "%s  %s·%s  %s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Title)
		}
		if len(ms) == 0 {
			fmt.Fprintln(a.stdout, "찾은 기억이 없습니다.")
		}
		return nil
	case "show":
		fs := a.flags("memory show")
		history := fs.Bool("history", false, "버전 이력")
		asJSON := fs.Bool("json", false, "JSON")
		pos, err := parse(fs, args[1:])
		if err != nil || len(pos) != 1 {
			return errUsage
		}
		m, err := s.Get(pos[0])
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(m)
		}
		fmt.Fprintf(a.stdout, "%s · %s · %s · %s · v%d\n%s\n\n%s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Scope, m.Version, m.Title, m.Body)
		if len(m.Tags) > 0 {
			fmt.Fprintln(a.stdout, "\n태그: "+strings.Join(m.Tags, ", "))
		}
		for _, src := range m.Sources {
			fmt.Fprintf(a.stdout, "출처 %s %s %s\n", src.Kind, src.Ref, oneLine(src.Excerpt, 80))
		}
		if *history {
			h, _ := s.History(m.ID)
			for _, v := range h {
				fmt.Fprintf(a.stdout, "v%d %s %s %s\n", v.Version, time.UnixMilli(v.At).Format("2006-01-02 15:04"), v.Action, v.Reason)
			}
		}
		return nil
	}
	return errUsage
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func printProposal(w io.Writer, s *memory.Store, p memory.Proposal, full bool) {
	fmt.Fprintf(w, "%s  [%s] %s\n", p.ID, p.Kind, p.Title)
	for _, id := range p.MemoryIDs {
		if m, err := s.Get(id); err == nil {
			fmt.Fprintf(w, "    대상 %s %s·%s %s\n", m.ID, kindName(m.Kind), stateName(m.State), m.Title)
		}
	}
	if full && p.Body != "" {
		fmt.Fprintln(w, "    "+strings.ReplaceAll(p.Body, "\n", "\n    "))
	}
}

func cmdProposals(a *app, args []string) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := a.flags("proposals " + sub)
	all := fs.Bool("all", false, "결정한 제안도")
	title := fs.String("title", "", "고쳐서 승인할 제목")
	body := fs.String("body", "", "고쳐서 승인할 본문")
	bodyFile := fs.String("body-file", "", "고쳐서 승인할 본문 파일")
	note := fs.String("note", "", "결정 이유")
	asJSON := fs.Bool("json", false, "JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		status := memory.StatusPending
		if *all {
			status = ""
		}
		ps, err := s.Proposals(status)
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(ps)
		}
		for _, p := range ps {
			printProposal(a.stdout, s, p, false)
		}
		fmt.Fprintf(a.stdout, "대기 %d건. 자세히: oni proposals show <id> · 하나씩 결정: oni proposals review\n", s.PendingCount())
		return nil
	case "show":
		if len(pos) != 1 {
			return errUsage
		}
		p, err := s.GetProposal(pos[0])
		if err != nil {
			return err
		}
		if *asJSON {
			return a.json(p)
		}
		printProposal(a.stdout, s, p, true)
		return nil
	case "approve", "reject":
		if len(pos) != 1 {
			return errUsage
		}
		in := memory.DecideInput{ID: pos[0], Decision: sub, Note: *note}
		if *title != "" {
			in.Title = title
		}
		if *bodyFile != "" {
			b, err := os.ReadFile(*bodyFile)
			if err != nil {
				return err
			}
			text := string(b)
			in.Body = &text
		} else if *body != "" {
			in.Body = body
		}
		res, err := s.Decide(in)
		if err != nil {
			return err
		}
		return a.reportDecision(res)
	case "review":
		return a.review(s)
	}
	return errUsage
}

func (a *app) reportDecision(res memory.DecideResult) error {
	word := map[string]string{memory.StatusApproved: "승인", memory.StatusRejected: "거절"}[res.Proposal.Status]
	fmt.Fprintf(a.stdout, "%s %s했습니다.", res.Proposal.ID, word)
	if res.Created != "" {
		fmt.Fprintf(a.stdout, " 새 기억 %s.", res.Created)
	}
	if len(res.Changed) > 0 {
		fmt.Fprintf(a.stdout, " 바뀐 기억 %s.", strings.Join(res.Changed, ", "))
	}
	if res.File != "" {
		fmt.Fprintf(a.stdout, " 파일을 바꿨습니다(커밋은 직접): %s", res.File)
	}
	fmt.Fprintln(a.stdout)
	return nil
}

// review walks through pending proposals one by one on the terminal.
func (a *app) review(s *memory.Store) error {
	ps, err := s.Proposals(memory.StatusPending)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Fprintln(a.stdout, "대기 중인 제안이 없습니다.")
		return nil
	}
	in := bufio.NewReader(a.stdin)
	for i, p := range ps {
		fmt.Fprintf(a.stdout, "\n(%d/%d) ", i+1, len(ps))
		printProposal(a.stdout, s, p, true)
		fmt.Fprint(a.stdout, "승인 a · 거절 r · 넘김 s · 그만 q > ")
		line, err := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		switch answer {
		case "a", "r":
			decision := map[string]string{"a": "approve", "r": "reject"}[answer]
			res, derr := s.Decide(memory.DecideInput{ID: p.ID, Decision: decision})
			if derr != nil {
				fmt.Fprintln(a.stdout, "결정하지 못했습니다: "+derr.Error())
				continue
			}
			a.reportDecision(res)
		case "q":
			return nil
		}
		if err != nil {
			return nil
		}
	}
	fmt.Fprintf(a.stdout, "\n남은 제안 %d건\n", s.PendingCount())
	return nil
}

func cmdBackup(a *app, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s, err := openStore()
	if err != nil {
		return err
	}
	defer s.Close()
	path, err := s.Backup(paths().Backups(), cfg.BackupKeep)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "백업했습니다: "+path)
	return nil
}

func cmdConfig(a *app, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "show" {
		fmt.Fprintln(a.stdout, "# "+paths().Config())
		return a.json(cfg)
	}
	if args[0] != "set" || len(args) != 3 {
		return errUsage
	}
	key, val := args[1], args[2]
	switch key {
	case "retroHost":
		cfg.RetroHost = val
	case "retroModel":
		cfg.RetroModel = val
	case "retroHour":
		_, err := fmt.Sscanf(val, "%d", &cfg.RetroHour)
		if err != nil {
			return err
		}
	case "inject":
		cfg.Inject = val
	case "recentDecisions":
		if _, err := fmt.Sscanf(val, "%d", &cfg.RecentDecisions); err != nil {
			return err
		}
	case "backupKeep":
		if _, err := fmt.Sscanf(val, "%d", &cfg.BackupKeep); err != nil {
			return err
		}
	case "applyTargets":
		cfg.ApplyTargets = nil
		for _, p := range strings.Split(val, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.ApplyTargets = append(cfg.ApplyTargets, p)
			}
		}
	default:
		return fmt.Errorf("모르는 키입니다: %s (retroHost·retroModel·retroHour·inject·recentDecisions·backupKeep·applyTargets)", key)
	}
	if err := config.Save(paths().Config(), cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s = %s 로 저장했습니다.", key, val)
	if key == "retroHour" {
		fmt.Fprint(a.stdout, " 예약 시각을 바꾸려면 oni install --no-hosts 로 예약을 다시 등록하세요.")
	}
	fmt.Fprintln(a.stdout)
	return nil
}

var _ = context.Background
