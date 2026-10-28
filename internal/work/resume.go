package work

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ResumeContext lists the project's open works for the session-start context
// (설계 O9): each work's title with its in-progress and blocked steps, most
// recently changed first, until budget characters are used. Works left out
// are counted in a last line.
func (s *Store) ResumeContext(projectScope string, budget int, opt ReadOptions) string {
	if projectScope == "" || budget <= 0 {
		return ""
	}
	rd := s.reader(opt)
	rows, err := rd.works(`closed_at=0 AND project_scope=? ORDER BY updated_seq DESC, id`, projectScope)
	if err != nil || len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Orbit 작업 · 열린 작업] 이 프로젝트에서 끝나지 않은 작업이다. 이어 하려면 work_get으로 읽는다.\n")
	left := 0
	for i, w := range rows {
		g, err := rd.load(w)
		if err != nil {
			continue
		}
		head, steps, _ := rd.derive(g)
		var active, blocked []string
		for _, st := range steps {
			switch st.Status {
			case "in_progress":
				active = append(active, clip(st.Title, 60))
			case "blocked":
				blocked = append(blocked, clip(st.Title, 60)+"("+blockName[st.Block.Reason]+")")
			}
		}
		line := fmt.Sprintf("- %s (%s) · 단계 %d개", clip(head.Title, TitleMax), w.id, len(steps))
		if len(active) > 0 {
			line += " · 진행: " + strings.Join(active, ", ")
		}
		if len(blocked) > 0 {
			line += " · 막힘: " + strings.Join(blocked, ", ")
		}
		line = clip(line, 400) + "\n"
		if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(line) > budget {
			left = len(rows) - i
			break
		}
		b.WriteString(line)
	}
	if left > 0 {
		b.WriteString(fmt.Sprintf("- 그 밖에 %d건(work_list로 찾는다)\n", left))
	}
	return strings.TrimSpace(b.String())
}

var blockName = map[string]string{"waiting_user": "사용자 응답 대기", "run_failed": "맡긴 실행 실패", "run_lost": "맡긴 실행 잃음",
	"interrupted": "중단", "stale": "응답 없음", "declared": "AI가 밝힌 막힘"}

// TitleRequest is the context a list tool's hook adds once when it opened a
// work (설계 O2).
func TitleRequest(workID string) string {
	return fmt.Sprintf("[Orbit 작업] 작업 %s에 제목이 없다. work_title(workId=%q)로 무엇을 하는 작업인지 한 줄 제목을 정하라. 작업의 목표가 바뀌면 다시 불러 고친다.", workID, workID)
}
