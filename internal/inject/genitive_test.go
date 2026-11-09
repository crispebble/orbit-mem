package inject

import (
	"strings"
	"testing"

	"github.com/crispebble/orbit-mem/internal/config"
	"github.com/crispebble/orbit-mem/internal/memory"
)

// 「한 글자 명사+의」(1-6 b6rs): 요청의 「법의」는 저장소에 통째 낱말 「법」이 있으면 「법」으로 뗀다. 진단(why)은 회상과
// 같은 낱말로 재고, 같은 판정을 낸다.
func TestDiagnose_한_글자_명사_의는_회상과_같은_낱말로_잰다(t *testing.T) {
	s := store(t)
	m := save(t, s, memory.KindFact, "법 조항은 원문으로 인용한다", "", memory.ScopeGlobal, false)
	save(t, s, memory.KindFact, "방법의 배포 커밋 포트 화면 백업 로그 시험 문서 설정은 따로 적는다", "", memory.ScopeGlobal, false)
	cfg := config.Default()

	// 낱말이 많아 맞은 비율 문턱에 걸리는 요청: 진단이 말하는 맞은 낱말·요청 낱말이 회상의 것과 같다.
	weak := "법의 조항 배포 커밋 포트 화면 백업 로그 시험 문서"
	words, err := s.RecallWords(weak)
	if err != nil {
		t.Fatal(err)
	}
	if words[0] != "법" {
		t.Fatalf("회상 낱말 %v", words)
	}
	st := stages(Diagnose(s, cfg, DiagnoseInput{ID: m.ID, Scope: here, Prompt: weak}))[StageSelected]
	if st.Code != "weak_match" || st.Args[0] != "법 조항" || st.Args[1] != strings.Join(words, " ") {
		t.Fatalf("진단 %+v, 회상 낱말 %v", st, words)
	}
	if strings.Contains(PromptInjection(s, cfg, here, weak).Text, m.ID) {
		t.Fatal("진단은 빠진다고 했는데 회상에 들었습니다")
	}

	// 문을 넘는 요청: 진단과 회상이 함께 넣는다.
	pass := "법의 조항 원문 인용"
	if d := Diagnose(s, cfg, DiagnoseInput{ID: m.ID, Scope: here, Prompt: pass}); stages(d)[StageSelected].Code != "matched" {
		t.Fatalf("진단 %+v", stages(d)[StageSelected])
	}
	if !strings.Contains(PromptInjection(s, cfg, here, pass).Text, m.ID) {
		t.Fatal("진단은 넣는다고 했는데 회상에 없습니다")
	}
}
