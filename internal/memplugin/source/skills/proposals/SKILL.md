---
name: proposals
description: orbit-mem 감사 목록(자동 확정·자동 정리·자동 고치기, 어긋남, 반복 교정)을 사용자와 함께 보고 틀린 것을 되돌릴 때 쓴다. 사용자가 "감사 목록 보여줘", "자동으로 바뀐 것 보여줘", "회고 결과 보여줘", "제안 보여줘"라고 할 때만.
---

# 감사 목록

사람 승인 대기열은 없다. AI가 배운 것은 증거가 쌓이면 스스로 확정되고, 오래 쓰지 않은 후보는 낡음이 된다. orbit-code 회고의 정리(낡음·합치기·대체·잇기·태그·고치기·재분류)는 승인 없이 적용돼 변경 효과 원장에 남는다. 사람은 보고 싶을 때 감사 목록을 보고 **틀린 것만 되돌린다.** 사용자가 직접 말한 기억은 자동화가 고치지 못한다. 그런 기억을 바꿔야 하면 일하는 중에 사용자에게 직접 묻고, 그 말을 `userQuote`로 준다.

먼저 꺼내지 않는다. 사용자가 물을 때만 보인다.

1. `proposals`를 부른다(기본이 감사 목록, `status=audit`과 같다).
2. 감사 목록을 세 묶음으로 보인다. 묶음마다 머리에 이름과 건수를 쓴다.
   - 자동 변경(`changes`): 종류(confirm·merge·stale·supersede·link·retag·edit·reclassify·apply_file·revert), 제목, 확정이면 증거(`seen_days` 다른 날 다시 나옴, `followed_verified` 검증된 일에서 따름, `verified_fact` 작업이 검증한 사실), 효과 판정(관찰 중·도움됨·해로움·효과 없음·섞임), 날짜. 해로움으로 판정돼 스스로 되돌린 것(`autoReverted`)은 따로 짚는다.
   - 어긋남(`conflicts`): 후보 제목과 어긋나는 사용자 규칙 제목, 표시한 쪽과 이유.
   - 반복 교정(`corrections`): 대상(기억·스킬·역할), 교정이 나온 세션 수와 기준, 최근 사용자 말. 스킬·역할, 서로 다른 작업 여럿에서 나온 절차(`procedure`)면 「하네스 개선 후보」로 짚는다(고치는 일은 orbit-code 개발 과정이다. 이 PC에서는 교정을 기억으로 바로 따른다).
   - 기준 횟수(`policy`)가 사용자 결정 대기(`policyPending`)면 그 사실을 한 줄로 알린다.
   - 대기열을 없애기 전에 남은 옛 제안 수(`pending`)가 있으면 한 줄로 알리고, 정리는 `orbit-mem proposals drain`(먼저 `orbit-mem backup`, 미리 보기는 `--dry-run`)으로 한다고 안내한다.
3. 사용자의 답을 받은 뒤에만 `proposal_decide`를 부른다.
   - 자동 변경이 틀렸으면 `decision=revert`, `id`에 그 `proposalId`(또는 `ledgerId`), `note`에 사용자가 말한 이유. 이전 판으로 돌아가고(자동 확정은 후보로, 자동 낡음은 다시 살아남), 같은 변경은 자동화가 다시 하지 않는다. 파일 반영을 되돌리면 파일만 바뀐다. 커밋은 사용자가 한다.
   - 어긋남: 후보가 맞으면 `memory_update`(`action=confirm`, 사용자 말을 `userQuote`에), 틀리면 `action=stale`, 문제없다고 하면 `decision=dismiss`(`id`에 `f-…`). 어긋남 표시를 닫을 때는 사용자가 그렇게 하라고 한 말을 `userQuote`에 그대로 넣는다.
   - 반복 교정을 봤다고 하면 `decision=dismiss`(`id`에 `correction:…` 키). 그 뒤 교정만 다시 센다.
   - 같은 답을 받은 항목 여럿은 `ids`에 모아 한 번에 결정하고, 실패한 것은 이유와 함께 그대로 알린다.
   - 답이 모호하거나 어느 항목을 가리키는지 분명하지 않으면 다시 묻는다.
4. 끝나면 되돌린 수, 닫은 수와 남은 것을 알린다.

터미널에서는 `orbit-mem audit`(되돌리기 `audit revert <id>`, 확인함 `audit dismiss <id>`)으로 같은 일을 할 수 있다.
