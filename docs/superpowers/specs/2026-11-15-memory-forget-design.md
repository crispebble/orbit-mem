# 명세 · 기억 완전 삭제 (`memory_forget`)

2026-11-15 · crispebble과 정함. 상태: 승인(2026-11-15).

## 1. 왜

- orbit-mem에는 기억을 지우는 길이 없다. `stale`은 회상에서 빼기만 하고 행·이력은 남는다(README.md:112).
- 폐기한 일(orbit-cc)을 살아 있는 것처럼 적은 기억이 확정으로 남아 AI가 그것을 따르려 했다. 정리하려는 세션도 지울 수 없어 멈췄다.
- 사용자는 「안 보이게 치우기」가 아니라 「DB에서 완전 삭제」를 골랐다. AI도 지울 수 있되, 사용자 말을 받았을 때만 지운다. 새 도구 `memory_forget`으로 낸다.

## 2. 사전 조사

| 가정 | 판정 | 근거 |
| --- | --- | --- |
| 사용자가 쓸 수 있는 삭제 경로가 없다 | 확인함 | 도구 동작 목록 server.go:142, 명령 표 README.md:104-130. 행 삭제는 평가용 되감기 asof.go:392에만 있다 |
| 기억 id를 담는 표는 memories·versions·sources 정도다 | 다름 | 14곳 이상이다. 제목·본문 사본이 `memory_versions.snapshot`, `ledger.before`, `proposals.title/body`, `judgments.title/excerpt`, `events.data`(correction·memory.used의 quote), 다른 기억의 `stale_reason`, `session_bundles.text`에 있다(store.go:39-105, ledger.go:253, supersede.go:64) |
| 되감기의 삭제 코드를 그대로 쓸 수 있다 | 다름 | 표 4개만 지운다. 색인 지우기 `unindexTx`(asof.go:440-470)만 다시 쓴다 |
| 사용자 말 확인은 `needsWords`를 쓰면 된다 | 다름 | 제안 id용이다. 제안이 없으면 검사 없이 통과시킨다(server.go:1240-1243). 새 검사가 필요하다 |
| 도구를 더하면 호스트 계약 판이 바뀐다 | 다름 | 계약에는 도구 목록 파일이 없다. `HostTools`(tools.go:123)에 넣지 않으면 계약은 그대로다. `proposals`·`proposal_decide`도 거기 없다 |
| 되돌릴 수 없는 도구 표시의 선례가 있다 | 확인함 | `proposal_decide`가 `writeHints(true, true)`(server.go:150) |
| 지운 기억이 되살아날 길이 있다 | 확인함 | `import`는 같은 id가 없으면 다시 넣는다(transfer.go:68-74). `migrate`는 `legacy_id`로만 거른다(migrate.go:218). `backup restore`는 DB 전체를 덮는다(backup.go:296-340) |
| 백업에 지운 기억이 남는다 | 확인함 | 하루 사본 14개, `before-*` 사본은 지우지 않는다(README.md:292-293) |
| 플러그인 파일에 도구 이름 목록이 있다 | 다름 | `plugin.json`·`hooks.json`에 도구 이름이 없다. 원본은 `internal/memplugin/source/`, `plugin/`은 `plugin build`가 만드는 생성물이다 |
| 사용자 호스트 설정이 orbit-mem 도구를 와일드카드로 허용하는지 | 확인 못 함 | 저장소 밖이다. 허용돼 있으면 `userQuote` 검사가 마지막 방어선이다 |
| tools/list 8,000자 상한의 남은 여유 | 확인 못 함 | 시험을 돌려 보지 않았다(surface_test.go:422-443). MCP 안내는 919자, 상한 1,200자다 |

### 2.1 다른 기억 시스템 (2026-11-15 조사)

| 시스템 | 잊기·삭제 구분 | AI가 지우나 | 삭제 뒤 남는 것 |
| --- | --- | --- | --- |
| mem0 | 삭제만(`delete`·`batch_delete`) | 호출한 쪽이 지움. 확인 장치 없음 | history에 `DELETE` 행과 지운 내용(`prev_value`). 현재 판을 지우면 옛 판이 되살아나 `delete_linked`를 더함(PR #5270) |
| Zep/Graphiti | 무효화(`invalid_at`)와 삭제가 따로 | MCP 서버는 권한 없이 누구나 지움 | 지운 에피소드 내용이 요약에 남는 버그가 반복됨(#1837·#2034) |
| LangMem | 구현에 맡김 | `actions_permitted`로 delete를 도구에서 뺄 수 있음 | 저장소에 따름 |
| Letta | 블록 떼기와 삭제가 따로 | 보관 기억은 에이전트가 쉽게 못 지움. 개발자가 SDK로 지움 | 문서에 없음 |
| supermemory | forget(검색에서 뺌, 감사 이력 보존)과 삭제가 따로 | 의미 기반 삭제는 `dryRun` 필수 | forget은 보존 |
| Claude 앱 | 일시중지와 초기화(영구 삭제)가 따로 | 사용자만 | 대화를 지워도 기억은 남음 |

시사점: 업계의 보호책은 대개 「AI에게 delete를 주지 않음」이다. 사용자 말을 받아야 지우는 장치의 선례는 찾지 못했다. 지운 id의 재유입을 막는 장치(`forgotten`)도 찾지 못했다. orbit-mem은 `stale`(잊기)과 `memory_forget`(삭제)으로 두 동작을 갖게 된다.

## 3. 목표 · 비목표 · 실패 조건

목표
- AI가 사용자 말을 근거로 기억 1~20개를 한 번에 DB에서 지운다.
- 사람은 명령줄 `orbit-mem forget`으로 지운다.
- 지운 기억은 회상·검색·`memory_get`·내보내기·감사 목록 어디에도 나오지 않는다.
- 지운 기억이 들여오기·옮기기·저장 재시도로 되살아나지 않는다.

비목표
- 비밀값 완전 소거. SQLite 빈 쪽·WAL·FTS 조각, 백업 사본, 사용자가 내보낸 파일, 이미 모델 문맥에 들어간 글은 다루지 않는다. 문서에 이 한계를 적는다.
- 백업 사본에서 지우기. 하루 사본이 14일 동안 남는 것이 잘못 지웠을 때의 복구 수단이다.
- 이미 연 세션에 「이 기억이 지워졌다」고 알리기.
- 지운 기억에서 나눠 만든 조각(split)이나 다른 기억에 복사된 글을 따라가 지우기.
- 호스트 계약(`HostTools`, `orbit-mem tool`, host `call`)에 `memory_forget`을 더하기.
- 이 기능이 없는 옛 판 실행 파일로 들여오기·옮기기를 할 때 되살아남을 막기. 스키마 판을 올리지 않으므로(5.4) 옛 판은 `forgotten`을 모른다.

실패 조건(돌아가도 실패로 본다)
- AI가 `userQuote` 없이 지울 수 있다.
- 하루 정리나 자동 변경이 기억을 지울 수 있다.
- 일괄 삭제가 중간에 실패해 일부만 지워진다.
- 지운 뒤 다른 기억의 `memory_get`·회상·하루 정리·감사 되돌리기가 오류로 멈춘다.
- 지운 기억이 FTS 검색이나 `export`에 나온다.
- 옛 export를 `import`하면 지운 기억이 돌아온다.
- 도구 설명이 「되돌릴 수 있음」으로 표시된다.

## 4. 고른 방식 (Approaches)

| 안 | 장점 | 단점 | 결과 |
| --- | --- | --- | --- |
| A. 새 도구 `memory_forget` | 되돌릴 수 없음 표시를 정직하게 단다. 여러 개를 한 번에 지운다. `proposal_decide`와 같은 꼴이다 | 도구 목록 시험·안내 문구를 고쳐야 한다 | 고름 |
| B. `memory_update`에 `action=delete` | 고칠 곳이 적다 | 편집 도구의 표시가 틀리거나, 모든 편집이 위험 작업이 된다. 한 번에 하나만 지운다 | 버림 |
| C. 사람만 명령줄로 | 가장 안전하다 | 휴대폰·대화 중 정리가 안 된다 | 사용자가 버림 |

## 5. 동작

### 5.1 MCP 도구 `memory_forget`

- 입력: `ids`(1~20개), `userQuote`, `reason`.
- 표시: `writeHints(destructive=true, idempotent=false)`.
- 설명: 「기억을 DB에서 완전히 지운다(되돌릴 수 없다). 틀리거나 낡은 기억은 먼저 memory_update의 stale·supersede로 고친다. 사용자가 지우라고 한 말을 userQuote로 줄 때만 부른다.」 잊기(`stale`)와 삭제를 도구 설명에서 나눈다(supermemory의 forget/delete 구분과 같은 효과).
- `userQuote`·`reason`은 길이를 제한하고 비밀값 검사(`MaskSecrets`)를 거친다. `forgotten` 표에 지우려던 비밀이 다시 들어가지 않게 한다. `memory_save`의 다른 거부(지시문·진행 상태)는 「지워」 같은 진짜 사용자 말을 막을 수 있어 쓰지 않는다.
- 쓰는 쪽 판정은 서버 옵션의 `Who`로 한다.
  - `ai`: `userQuote`가 비면 거절한다(`ErrProtected` 계열).
  - `automation`: 늘 거절한다.
  - `user`: 통과한다.
- 한 트랜잭션에서 처리한다. 하나라도 없는 id가 있으면 아무것도 지우지 않고 없는 id를 알린다.
- 결과: 지운 id 목록, 함께 고친 다른 기억 id(5.3), 「백업 사본과, 다른 기억에 나눠지거나 복사된 글은 지우지 않았다」는 안내 한 줄. Graphiti가 지운 내용이 요약에 남는 버그를 거듭 겪었다(이슈 #1837·#2034). 그러니 남는 곳을 숨기지 않고 알린다.
- 호출 기록(`tool.memory_forget` 이벤트)에는 개수만 넣는다. `userQuote`·`reason`·제목은 넣지 않는다.

### 5.2 명령줄 `orbit-mem forget`

명령: `orbit-mem forget <id>… [--reason 글] [--quote 글] [--yes]`

- 지울 기억의 id와 제목을 먼저 보인다.
- 터미널이면 확인을 묻는다. 터미널이 아니면 `--yes` 없이는 지우지 않고 종료 1이다(`backup restore`와 같은 흐름).
- 쓰는 쪽은 다른 명령과 같이 `actorOf`(`--actor`, `ORBIT_ACTOR`, 기본 user)로 정한다(cmd/orbit-mem/memory_cmds.go:57-59). `ai`면 `--quote`가 있어야 지운다. `automation`이면 거절한다. 셸로 부르는 AI가 사용자 말 없이 지우는 길을 이것으로 좁힌다. `core --pin`과 같은 꼴이다(README.md:237). `ORBIT_ACTOR`를 붙이지 않은 셸 호출이 사람으로 쓰이는 한계(README.md:270)는 남는다.

### 5.3 지우는 범위

행 삭제:
`memories`, `memory_versions`, `sources`, `sightings`, `memory_links`(양 끝, 뗀 줄 포함), `memory_fts`·`memory_index`·`memory_index_late`, `judgments`, `audit_flags`(memory_id·other_id), `session_marks`, `activity`의 일지 이동 줄, `events` 중 correction·memory.used로 그 기억을 가리키는 줄.

글만 비움(줄은 남김). 감사 목록과 해로운 자동 변경 되돌리기가 `ledger JOIN proposals`로 읽고(audit.go:529, auto.go:337) 자동 변경 판정이 제안에 기대므로(auto.go:261-265), 줄을 지우면 살아 있는 다른 기억의 감사 항목까지 사라진다. 그래서 줄은 남기고 글만 바꾼다.
- 대상 원장 줄: `targets`·`observe`·`before`에 그 id가 든 줄, 그리고 `revert_of`·`reverted_by`로 그 줄과 이어진 줄.
- 대상 제안 줄: `memory_ids`에 그 id가 든 줄, 대상 원장 줄과 짝인 제안 줄. 되돌림 제안은 `memory_ids`가 비어 있으므로(auto.go:283) 원장 쪽에서 찾는다.
- 원장: `before.Memories`에서 그 기억의 스냅숏을 뺀다. `Created`·`targets`·`observe`의 id는 남긴다. `change`(줄 단위 비교, ledger.go:221·325-333)와 `basis`(제안 제목·본문, ledger.go:365)를 「지운 기억 <id>에 대한 변경」으로 바꾼다.
- 「지운 기억이 있어 되돌릴 수 없음」 표시는 지운 id가 `before.Memories`나 `Created`에 있던 줄에만 단다. `observe`에만 있던 줄(지침 파일 변경 등)은 글만 비우고 되돌리기를 그대로 허용한다. 표시된 줄을 되돌리려 하면 그 까닭을 사람이 읽을 글로 알린다.
- 대기 중인 제안이 지운 id를 담고 있으면 「지운 기억」 사유로 닫는다(`StatusDropped`).
- 제안: `title`·`body`·`payload`를 「지운 기억 <id>에 대한 제안」과 빈 값으로 바꾼다. 여러 기억에 걸친 제안이면 살아 있는 기억에 대한 설명도 함께 사라진다. id 목록은 남으므로 그 기억들의 감사 항목은 남는다.
- `events`의 `audit.auto_reverted`(auto.go:26·363) 제목. `ledgerId`는 이미 되돌리려 했는지 가리는 데 쓰므로(auto.go:369-373) 그대로 둔다.

다른 기억:
- 지운 기억이 다른 기억을 대체했으면, 그 옛 판의 지금 행에서 `superseded_by`를 비우고 낡음 사유를 「대체한 기억을 지웠다」로 바꾼다. 옛 판은 낡음 상태 그대로 둔다. 옛 판의 판 이력은 고쳐 쓰지 않는다.

id만 남김: `events`의 tool·inject·linked·unlinked·decided, `audit_acks.key`, `save_requests`, `session_bundles`(세션 캐시를 깨지 않으려고 그대로 둔다).

### 5.4 되살아남 막기

- 새 표 `forgotten(id, legacy_id, at, actor, reason, quote)`. 제목·본문은 넣지 않는다.
- 스키마 판(`user_version`)은 올리지 않는다. 지금까지 표·열을 더한 방식(README.md:256, `CREATE IF NOT EXISTS`)을 따른다. 판을 올리면 옛 실행 파일이 DB를 열지 못한다(store.go:222-224).
- `import`는 `forgotten`에 있는 id나 `legacy_id`를 「지운 기억」으로 건너뛰고 보고한다. 들여오는 기억의 `superseded_by`가 지운 id면 비우고 들여온다.
- `migrate`는 `forgotten`의 `legacy_id`를 건너뛴다(migrate.go:218).
- 저장 재시도: 같은 요청 키로 다시 온 저장은 처음 요청이 이미 성공한 것이다. 그 기억이 지워졌으면 재생(idem.go:85-99·133-147)은 새로 저장하지 않고 「이 요청으로 만든 기억은 지웠다」로 거절한다. `update`·`skip`·`supersede`로 기록된 키도 같다. `save_requests` 줄은 그대로 두고 30일 정리에 맡긴다. 같은 내용을 새 요청 키로 저장하는 것은 막지 않는다.
- `backup restore`는 사본 시점으로 되돌리는 일이라 지운 기억도 돌아온다. 이것이 잘못 지웠을 때의 복구 수단이다. 복구 출력에 「사본에 있던, 그 뒤에 지운 기억 N개가 돌아온다」를 알린다.

### 5.5 문서·스킬

- README: 명령 표, MCP 도구 목록, 「지우지 않는다」 문구, 백업 절에 「지운 기억은 백업에 남는다」를 더한다.
- `internal/memplugin/source/skills/memory/SKILL.md`에 지우기 규칙을 더하고 `plugin build --repo .`로 생성물을 다시 만든다.
- MCP 안내 문자열에 한 줄을 더한다. 1,200자 상한 안에서 쓴다.
- 영어·한국어 `cli.json`·`memory.json`에 문구를 더한다.

## 6. 수용 기준

1. AI가 `userQuote`와 함께 기억 3개를 지우면, 셋 다 `memory_get`이 없음을 돌려주고 `memory_find`·FTS·`export`에 나오지 않는다.
2. AI가 `userQuote` 없이 부르면 거절되고 아무것도 지워지지 않는다.
3. 쓰는 쪽이 `automation`이면 `userQuote`가 있어도 거절된다.
4. `ids`가 0개이거나 21개면 거절된다.
5. 없는 id가 하나 섞이면 아무것도 지워지지 않고 그 id가 보고된다.
6. 지운 뒤 5.3의 행 삭제 표에서 그 id로 찾으면 0행이다.
7. 연결된 다른 기억의 `memory_get`이 성공하고 links에서 지운 기억이 빠진다.
8. 새 판을 지우면 옛 판의 `superseded_by`가 비고 옛 판은 낡음으로 남는다. 회상에 옛 판이 돌아오지 않는다.
9. 상시 핵심에 올린 기억을 지우면 다음 세션 시작 묶음에서 빠진다.
10. 지운 기억이 든 원장 줄을 `audit revert`하면 까닭이 적힌 오류가 나고, 하루 정리는 멈추지 않는다.
11. 지우기 전 export를 `import`하면 그 기억이 「지운 기억」으로 건너뛰어진다.
12. 명령줄에서 터미널이 아니고 `--yes`가 없으면 지우지 않고 종료 1이다.
12-1. 명령줄에서 `ORBIT_ACTOR=ai`이고 `--quote`가 없으면 `--yes`가 있어도 지우지 않는다.
12-2. 지운 기억이 든 원장 줄과 제안 줄은 감사 목록에 남되, 지운 기억의 제목·본문은 감사 목록 출력 어디에도 없다.
12-3. 지운 기억을 만든 저장 요청을 같은 요청 키로 다시 보내면 「지웠다」로 거절되고 새 기억이 생기지 않는다.
13. tools/list에 `memory_forget`이 `destructiveHint=true`로 나오고, 도구 목록·안내 문구 상한 시험이 통과한다.
14. `HostTools`와 호스트 계약 짝 파일이 바뀌지 않는다.
15. 전체 시험이 실패 0개다.

## 7. 내가 정한 것 (승인 때 확인)

- 백업은 건드리지 않는다. `restore`로 되살아나는 것은 복구 수단으로 둔다.
- 비밀값 완전 소거는 범위 밖이다.
- 원장 줄은 지우지 않고 그 기억의 스냅숏만 비운다. 감사 흔적은 남고 그 줄은 되돌릴 수 없게 된다.
- 다른 기억의 판 이력에 남은 지운 기억의 제목은 고쳐 쓰지 않는다.
- 이미 연 세션의 묶음 글은 그대로 둔다.
- 일괄 삭제는 전부 아니면 전무다.
- `forgotten` 표에 사용자 말과 이유를 남긴다. 제목·본문은 남기지 않는다.
- 원장·제안 줄은 지우지 않고 글만 바꾼다. 여러 기억에 걸친 제안은 살아 있는 기억에 대한 설명도 사라진다.
- 「되돌릴 수 없음」은 그 기억의 스냅숏이 있던 원장 줄에만 단다. 관찰 대상으로만 담긴 줄(지침 파일 변경)은 되돌릴 수 있게 둔다.
- 옛 판 실행 파일은 원장의 되돌릴 수 없음 표시를 모르므로, 그것으로 `audit revert`를 하면 남은 스냅숏만 되돌린다. 위험으로 적어 둔다.
- 명령줄도 `ORBIT_ACTOR=ai`면 `--quote`를 요구한다.
- 스키마 판은 올리지 않는다. 옛 판 실행 파일로 들여오면 되살아날 수 있다(비목표).
- 지운 기억을 만든 저장 요청을 같은 키로 재시도하면 거절한다.

## 8. 빌더에게 남긴 것

- 코드 배치(`internal/memory/forget.go` 등), 스키마 상수를 더하는 자리, 오류 문구.
- 새 표를 더할 때 `before-schema-*` 사본이 생기는지 확인하고 README에 맞춰 적는다.
- `retros`·`session_ends`·`lab_*` 옛 표에 기억 id가 글로 남는지 확인하고, 남으면 보고한다.
- 버전 올리기와 릴리스는 이 명세 밖이다.
