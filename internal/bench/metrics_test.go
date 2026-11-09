package bench_test

import "testing"

func TestHit는_A층과_B1_앞_3개와_B2의_합집합으로_잰다(t *testing.T) {
	rep, _ := runFake(t, `
	 {"id":"h1","session":"s1","prompt":"p1","expect":["a","b"]},
	 {"id":"h2","session":"s1","prompt":"p2","expect":["c"]},
	 {"id":"h3","session":"s2","tool":"t3","expect":["e"]},
	 {"id":"h4","session":"s2","prompt":"p4"}`, script{
		"start:s1": {[]string{"a"}, 50},
		"req:p1":   {[]string{"f", "g", "b"}, 200},
		"req:p2":   {[]string{"d", "h", "i", "c"}, 300}, // c는 넷째: 보인 것이 아니다
		"tool:t3":  {[]string{"e"}, 80},
	}, keyed{})
	m := rep.Runs[0].All.Requests
	if m.Cases != 4 {
		t.Errorf("cases = %d", m.Cases)
	}
	wantRate(t, "hit", m.Hit, 2, 3, f64(0.6667))
	if m.HitByStart != 1 {
		t.Errorf("hitByStart = %d, want 1 (h1 needs a from layer A)", m.HitByStart)
	}
	// 넣은 id: h1 3개(맞음 1) + h2 4개(맞음 1: c도 넣긴 했다) + h3 1개(맞음 1). 틀림 = 2 + 3 + 0.
	if m.Injected != 8 || m.Correct != 3 || m.Neutral != 0 {
		t.Errorf("injected %d correct %d neutral %d", m.Injected, m.Correct, m.Neutral)
	}
	wantRate(t, "wrong", m.Wrong, 5, 8, f64(0.625))
	wantRate(t, "empty", m.Empty, 1, 1, f64(1))
	wantDist(t, "b1 chars", m.Chars, 3, 166.6667, 200, 300)
	wantDist(t, "b2 chars", m.B2Chars, 1, 80, 80, 80)
	wantDist(t, "session chars", m.SessionChars, 2, 315, 315, 550) // s1 50+200+300, s2 0+80+0
	wantDist(t, "start chars", m.StartChars, 2, 25, 25, 50)
}

func TestWrong은_허용을_중립으로_센다_forbid는_따로_센다_같은_id는_한_번(t *testing.T) {
	rep, _ := runFake(t, `
	 {"id":"n1","session":"s1","prompt":"q1","expect":["a"],"allow":["b"],"forbid":["c"]},
	 {"id":"n2","session":"s1","prompt":"q2","tool":"t2","expect":["a"]}`, script{
		"req:q1":  {[]string{"a", "b", "c", "d"}, 400},
		"req:q2":  {[]string{"a"}, 100},
		"tool:t2": {[]string{"a"}, 100},
	}, keyed{})
	m := rep.Runs[0].All.Requests
	if m.Injected != 5 || m.Correct != 2 || m.Neutral != 1 {
		t.Errorf("injected %d correct %d neutral %d, want 5/2/1", m.Injected, m.Correct, m.Neutral)
	}
	wantRate(t, "wrong", m.Wrong, 2, 5, f64(0.4)) // c(forbid)와 d
	if m.Forbid != 1 || m.ForbidCases != 1 {
		t.Errorf("forbid %d in %d cases, want 1 in 1", m.Forbid, m.ForbidCases)
	}
	wantDist(t, "session chars", m.SessionChars, 1, 600, 600, 600) // 한 사례의 B1·B2는 둘 다 글자로 센다
}

func TestRecover는_아무것도_넣지_않은_사례와_맞추지_못한_사례_둘로_잰다(t *testing.T) {
	rep, _ := runFake(t, `
	 {"id":"r1","session":"s1","prompt":"m1","expect":["a"]},
	 {"id":"r2","session":"s1","prompt":"m2","expect":["b","c"]},
	 {"id":"r3","session":"s1","prompt":"m3","expect":["d"]},
	 {"id":"r4","session":"s1","tool":"t4","expect":["f"]},
	 {"id":"r5","session":"s1","prompt":"m5"},
	 {"id":"r6","session":"s1","prompt":"m6"}`, script{
		"req:m3": {[]string{"e"}, 40},
		"req:m6": {[]string{"g"}, 40},
	}, keyed{find: map[string][]string{
		"m1": {"i", "a", "j"}, // 8개 안에 정답 전부
		"m2": {"b"},           // 하나만
		"m3": {"d"},
		"m4": {"f"}, // 질의가 없는 도구 사례는 잰 값에 들지 않는다
	}})
	m := rep.Runs[0].All.Requests
	wantRate(t, "hit", m.Hit, 0, 4, f64(0))
	wantRate(t, "recover", m.Recover, 1, 2, f64(0.5))                // r1·r2(B1·B2가 비었다)
	wantRate(t, "recoverMissed", m.RecoverMissed, 2, 3, f64(0.6667)) // r1·r2·r3
	wantRate(t, "empty", m.Empty, 1, 2, f64(0.5))
}

func TestEmpty는_글자_0이어야_넣지_않음이다(t *testing.T) {
	rep, _ := runFake(t, `
	 {"id":"e1","session":"s1","prompt":"u1","origin":"agent"},
	 {"id":"e2","session":"s1","prompt":"u2","origin":"system"}`, script{
		"req:u2": {[]string{}, 12}, // id는 없지만 글자를 넣었다
	}, keyed{})
	wantRate(t, "empty", rep.Runs[0].All.Requests.Empty, 1, 2, f64(0.5))
}

func TestEmptyDenominators는_정한_대로_비운다(t *testing.T) {
	rep, _ := runFake(t, `{"id":"z1","session":"s1","prompt":"u1","origin":"agent"}`, script{}, keyed{})
	m := rep.Runs[0].All.Requests
	wantRate(t, "hit", m.Hit, 0, 0, nil)
	wantRate(t, "recover", m.Recover, 0, 0, nil)
	wantRate(t, "recoverMissed", m.RecoverMissed, 0, 0, nil)
	wantRate(t, "wrong", m.Wrong, 0, 0, f64(0))
	if !m.Wrong.ZeroDen {
		t.Error("wrong must say the denominator is 0")
	}
	sub, dup := rep.Runs[0].All.Subagent, rep.Runs[0].All.Duplicates
	wantRate(t, "subagent hit", sub.Hit, 0, 0, nil)
	wantRate(t, "duplicate hit", dup.Hit, 0, 0, nil)
	wantRate(t, "duplicate offered", dup.Offered, 0, 0, nil)
}

func TestToolOnly는_질의가_없어_놓침_복구에서_빠진다(t *testing.T) {
	rep, _ := runFake(t, `{"id":"t1","session":"s1","tool":"t","expect":["a"]}`, script{}, keyed{})
	wantRate(t, "recover", rep.Runs[0].All.Requests.Recover, 0, 0, nil)
	wantRate(t, "recoverMissed", rep.Runs[0].All.Requests.RecoverMissed, 0, 0, nil)
}

func TestSubagent와_Duplicate는_요청과_섞지_않고_따로_잰다(t *testing.T) {
	rep, calls := runFake(t, `
	 {"id":"rq1","session":"s1","prompt":"p1","expect":["a"]},
	 {"id":"sa1","session":"s9","kind":"subagent","task":"T1","expect":["a"],"allow":["b"],"forbid":["c"]},
	 {"id":"sa2","session":"s9","kind":"subagent","task":"T2"},
	 {"id":"dp1","kind":"duplicate","save":{"kind":"fact","title":"t1"},"expect":["a"]},
	 {"id":"dp2","kind":"duplicate","save":{"kind":"fact","title":"t2"},"allow":["b"]},
	 {"id":"dp3","kind":"duplicate","save":{"kind":"fact","title":"t3"}},
	 {"id":"dp4","kind":"duplicate","save":{"kind":"fact","title":"t4"},"expect":["d"],"forbid":["j"]}`, script{
		"req:p1": {[]string{"a"}, 10},
		"sub:T1": {[]string{"a", "b", "c"}, 120},
	}, keyed{sim: map[string][]string{
		"t1": {"a", "b", "c", "d"},
		"t2": {"b", "c"},
		"t4": {"a", "b", "c", "d", "j"}, // 넷째부터는 세지 않는다
	}})
	all := rep.Runs[0].All
	if all.Requests.Cases != 1 {
		t.Errorf("requests = %d, want 1", all.Requests.Cases)
	}
	s := all.Subagent
	wantRate(t, "subagent hit", s.Hit, 1, 1, f64(1))
	if s.Injected != 3 || s.Correct != 1 || s.Neutral != 1 || s.Forbid != 1 {
		t.Errorf("subagent %+v", s)
	}
	wantRate(t, "subagent wrong", s.Wrong, 1, 3, f64(0.3333))
	wantRate(t, "subagent empty", s.Empty, 1, 1, f64(1))
	wantDist(t, "subagent chars", s.Chars, 2, 60, 60, 120)
	d := all.Duplicates
	if d.Cases != 4 || d.Forbid != 0 {
		t.Errorf("duplicates %+v", d)
	}
	wantRate(t, "duplicate hit", d.Hit, 1, 2, f64(0.5))         // dp1, dp4(d는 넷째)
	wantRate(t, "duplicate offered", d.Offered, 1, 2, f64(0.5)) // dp2 c를 내밀었다, dp3은 없다
	for _, c := range *calls {
		if c == "start:dp1" {
			t.Errorf("a duplicate case has no session to start: %v", *calls)
		}
	}
}

// 위임 묶음은 핵심 제목 줄(Lead) 몫과 위임 회상 몫으로 나눠 센다. 틀린 회상·넣지 않음·글자는 회상 몫으로만 잰다.
var core = lead{[]string{"i"}, 40}

func TestSubagent는_핵심_제목_줄_몫과_위임_회상_몫을_나눠_잰다(t *testing.T) {
	rep, _ := runFakeLead(t, `
	 {"id":"sa1","session":"s1","kind":"subagent","task":"T1","expect":["a"],"allow":["b"],"forbid":["c"]},
	 {"id":"sa2","session":"s2","kind":"subagent","task":"T2"},
	 {"id":"sa3","session":"s3","kind":"subagent","task":"T3","expect":["i"]},
	 {"id":"sa4","session":"s4","kind":"subagent","task":"T4","expect":["a"]}`, script{
		"sub:T1": {[]string{"a", "b", "c"}, 120},
		"sub:T2": {nil, 40},
		"sub:T3": {nil, 40},
		"sub:T4": {[]string{"b"}, 50},
	}, leads{"sub:T1": core, "sub:T2": core, "sub:T3": core, "sub:T4": core}, keyed{})
	s := rep.Runs[0].All.Subagent
	wantRate(t, "hit", s.Hit, 2, 3, f64(0.6667)) // sa1은 회상, sa3은 핵심 제목 줄 덕에 맞았다
	if s.HitByLead != 1 {
		t.Errorf("hitByLead = %d, want 1", s.HitByLead)
	}
	if s.Injected != 4 || s.Correct != 1 || s.Neutral != 1 || s.Forbid != 1 || s.ForbidCases != 1 {
		t.Errorf("subagent %+v", s)
	}
	wantRate(t, "wrong", s.Wrong, 2, 4, f64(0.5)) // 핵심 id i는 틀린 회상이 아니다
	wantRate(t, "empty", s.Empty, 1, 1, f64(1))   // 제목 줄 40자는 넣은 것이 아니다
	wantDist(t, "recall chars", s.Chars, 4, 22.5, 5, 80)
	wantDist(t, "lead chars", s.LeadChars, 4, 40, 40, 40)
}

// 회상 몫에 든 핵심이 아닌 기억이 있으면 넣지 않음이 깨진다.
func TestSubagent는_회상_몫이_글자를_넣으면_넣지_않음이_아니다(t *testing.T) {
	rep, _ := runFakeLead(t, `{"id":"sa1","session":"s1","kind":"subagent","task":"T1"}`, script{
		"sub:T1": {[]string{"a"}, 90},
	}, leads{"sub:T1": core}, keyed{})
	wantRate(t, "empty", rep.Runs[0].All.Subagent.Empty, 0, 1, f64(0))
}

// 설계 L-2(같은 기억은 세션당 한 번)와 판정 전용 묶음 h-app-state-again 메모(「이미 넣었으면 다시 넣지 않아도 보인 것으로
// 센다」): 같은 세션 앞 요청들에서 B1이 넣은 id(넷째 이후도)는 뒤 요청에서 보인 것이다. 앞 요청의 B2·다른 세션의 B1은
// 아니고, 틀린 회상은 그 요청에 넣은 id로만 센다.
func TestHit는_같은_세션_앞_요청들의_B1이_넣은_것도_보인_것으로_센다(t *testing.T) {
	rep, _ := runFake(t, `
	 {"id":"e1","session":"s1","prompt":"p1","expect":["a"]},
	 {"id":"e2","session":"s1","prompt":"p2","expect":["d"]},
	 {"id":"e3","session":"s1","prompt":"p3","expect":["a","d","e"]},
	 {"id":"e4","session":"s1","prompt":"p4","tool":"t4","expect":["f"]},
	 {"id":"e5","session":"s1","prompt":"p5","expect":["f"]},
	 {"id":"e6","session":"s2","prompt":"p6","expect":["d"]}`, script{
		"req:p1":  {[]string{"a", "b", "c", "d"}, 100}, // d는 넷째: 이 요청에서는 보인 것이 아니지만 넣긴 했다
		"req:p2":  {[]string{}, 0},
		"req:p3":  {[]string{"e"}, 50},
		"req:p4":  {[]string{}, 0},
		"tool:t4": {[]string{"f"}, 80},
		"req:p5":  {[]string{}, 0},
		"req:p6":  {[]string{}, 0},
	}, keyed{})
	m := rep.Runs[0].All.Requests
	// e1 맞음(a는 첫째), e2 맞음(앞 요청이 넷째로 넣은 d), e3 맞음(a·d는 앞서, e는 이번), e4 맞음(B2),
	// e5 놓침(앞 요청의 B2는 세지 않는다), e6 놓침(다른 세션).
	wantRate(t, "hit", m.Hit, 4, 6, f64(0.6667))
	if m.HitByEarlier != 2 {
		t.Errorf("hitByEarlier = %d, want 2 (e2, e3)", m.HitByEarlier)
	}
	if m.HitByStart != 0 {
		t.Errorf("hitByStart = %d, want 0", m.HitByStart)
	}
	// 틀린 회상은 그 요청에 넣은 id로만: e1의 b·c·d(d는 e1의 정답이 아니다).
	if m.Injected != 6 || m.Correct != 3 {
		t.Errorf("injected %d correct %d, want 6/3", m.Injected, m.Correct)
	}
	wantRate(t, "wrong", m.Wrong, 3, 6, f64(0.5))
}

// 1-6 b6r 3차: 요청 훅의 「이 세션 중 바뀐 것」(B3, 세션 소식) 줄은 관련 기억 회상(B1)이 아니다. 소식 줄의 기억은 모델 앞에
// 놓였으니 적중의 「보인 것」에는 들지만, 틀린 회상·넣은 것·넣지 않음·요청당 글자는 B1 몫(+B2)만 센다. 소식은 따로 낸다.
// 소식 줄이 B1 앞자리를 차지해도 B1의 앞 3개는 회상 몫에서 센다.
func TestB3_세션_소식은_적중의_보인_것에만_들고_틀린_회상과_글자는_B1_몫만_센다(t *testing.T) {
	rep, _ := runFakeLead(t, `
	 {"id":"n1","session":"s1","prompt":"p1","expect":["a"]},
	 {"id":"n2","session":"s1","prompt":"p2","expect":["c"]},
	 {"id":"n3","session":"s1","prompt":"p3"}`, script{
		"req:p1": {[]string{"f", "g"}, 200},
		"req:p2": {[]string{"f", "g", "c"}, 300}, // c는 회상 몫 셋째: 소식 줄 셋 뒤에 와도 보인 것이다
		"req:p3": {[]string{}, 30},
	}, leads{
		"req:p1": {[]string{"d", "a", "e"}, 90},
		"req:p2": {[]string{"h", "i", "j"}, 60},
		"req:p3": {[]string{"b"}, 30},
	}, keyed{})
	m := rep.Runs[0].All.Requests
	wantRate(t, "hit", m.Hit, 2, 2, f64(1))
	if m.HitByNews != 1 || m.HitByStart != 0 || m.HitByEarlier != 0 {
		t.Errorf("hitByNews %d byStart %d byEarlier %d, want 1/0/0 (n1 needs the news line of a)", m.HitByNews, m.HitByStart, m.HitByEarlier)
	}
	// 넣은 것은 회상 몫만: f g + f g c. 맞음은 c 하나.
	if m.Injected != 5 || m.Correct != 1 || m.Neutral != 0 {
		t.Errorf("injected %d correct %d neutral %d, want 5/1/0", m.Injected, m.Correct, m.Neutral)
	}
	wantRate(t, "wrong", m.Wrong, 4, 5, f64(0.8))
	wantRate(t, "empty", m.Empty, 1, 1, f64(1)) // n3은 소식 줄만 넣었다: 회상은 넣지 않았다
	wantDist(t, "b1 chars", m.Chars, 3, 116.6667, 110, 240)
	n := m.News
	if n.Requests != 3 || n.IDs != 7 || n.Expected != 1 || n.Allowed != 0 {
		t.Errorf("news %+v, want 3 requests, 7 ids, 1 expected", n)
	}
	wantDist(t, "news chars", n.Chars, 3, 60, 60, 90)
	wantDist(t, "session chars", m.SessionChars, 1, 530, 530, 530) // 세션 누적에는 소식 글자도 든다
	c := rep.Runs[0].Cases[0].Request
	if c == nil || len(c.News) != 3 || c.NewsChars != 90 {
		t.Errorf("case n1 request %+v, want its 3 news ids and 90 news chars", c)
	}
}
