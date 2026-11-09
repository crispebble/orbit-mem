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
