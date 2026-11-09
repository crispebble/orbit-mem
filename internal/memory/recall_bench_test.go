package memory

import (
	"fmt"
	"path/filepath"
	"testing"
)

// 요청 한 번의 회상 시간(1-6 b6rs): 기억 1,200개 합성 저장소에서 「한 글자 명사+의」가 든 요청, 「의」로 끝나는 두 글자
// 낱말이 든 요청, 둘 다 없는 요청. 저장소 확인(「X의」를 뗄지)이 요청마다 기억 전부를 훑지 않는지 본다.
//
//	go test ./internal/memory -run '^$' -bench BenchmarkRecallHits -benchtime 200x
func BenchmarkRecallHits(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "memory.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	heads := []string{"법", "앱", "키", "표", "회의", "모의", "배포", "커밋", "포트", "화면", "백업", "로그", "시험", "문서", "설정"}
	verbs := []string{"규칙은", "순서를", "기록을", "꼴은", "주기는", "방식을", "이름은", "위치는"}
	tails := []string{"원문으로 남긴다", "다음 날 공유한다", "먼저 확인한다", "한 줄로 쓴다", "주마다 돌린다", "문서에 적는다"}
	for i := 0; i < 1200; i++ {
		title := fmt.Sprintf("%s %s %s %d", heads[i%len(heads)], verbs[i%len(verbs)], tails[i%len(tails)], i)
		body := fmt.Sprintf("합성 기억 %d번 본문: %s %s", i, heads[(i*7)%len(heads)], tails[(i*5)%len(tails)])
		if _, err := s.Save(SaveInput{Kind: KindFact, Title: title, Body: body, Scope: ScopeGlobal,
			Source: Source{Kind: SourceSession, Ref: "bench"}}); err != nil {
			b.Fatal(err)
		}
	}
	for _, c := range []struct{ name, prompt string }{
		{"한_글자_명사_의", "법의 규칙 원문 확인 방식 알려줘"},
		{"두_글자_낱말_의", "회의 기록 공유 방식 알려줘"},
		{"한_글자_명사", "법 규칙 원문 확인 방식 알려줘"}, // 「법의」를 뗀 뒤와 같은 낱말
		{"의_없음", "배포 순서 먼저 확인 방식 알려줘"},
	} {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.RecallHits(c.prompt); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
