package memory

// Outcomes of checking a memory whose source file changed (설계 · Orbit 3차
// O3-1 「출처 파일 판정」). The retrospectives wrote them as source.check events
// (removed 2026-11-08); orbit-mem stats still counts the old events.
const (
	SourceKept   = "kept"   // 발췌가 바뀐 파일에 그대로 있음: 해시만 갱신
	SourceAbsent = "absent" // 파일이 없어짐: 근거가 지워졌을 뿐이라 제안하지 않음(2026-10-30 사용자 결정)
	SourceGone   = "gone"   // 발췌가 파일에서 사라짐: 낡음 제안
	SourceOK     = "ok"     // 발췌 없음, 회고 모델 판정: 아직 맞음
	SourceEdit   = "edit"   // 회고 모델 판정: 고쳐야 함(edit 제안)
	SourceStale  = "stale"  // 회고 모델 판정: 낡음(낡음 제안)
)
