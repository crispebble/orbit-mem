---
name: memory
description: 이 PC의 Orbit 기억을 찾고 남기고 고칠 때 쓴다. 사용자가 "기억해", "이건 잊지 마", "전에 뭐라고 했지"라고 하거나, 다시 쓸 결정·교훈이 생겼을 때.
---

# Orbit 기억 쓰는 법

Orbit 기억은 이 PC에만 있다. 세션 시작에 확정 규칙이 들어오고, 요청과 강하게 맞는 기억은 제목과 id만 붙는다. 본문이 필요하면 `memory_get`으로 읽는다.

## 찾기

- 작업이 이전 결정·규칙·교훈에 기댈 것 같으면 `memory_find`로 찾는다. 기본 범위는 전역과 이 프로젝트다.
- 찾은 기억은 적용하기 전에 `memory_get`으로 본문과 출처를 읽는다. 후보(candidate)는 아직 확정이 아니다.

## 남기기

- 사용자가 "기억해"라고 하면 `memory_save`로 남긴다. 사용자의 말을 그대로 `source.kind=quote`, `source.excerpt`에 옮기면 바로 확정된다.
- 그 밖에 다시 쓸 사실·결정·교훈은 출처(file·command·session)와 함께 남긴다. 후보로 시작하고, 다른 날 다시 나오거나 사용자가 승인하면 확정된다.
- 종류: rule(사용자가 원하는 방식), fact, decision, lesson. 일지(journal)는 회고가 쓴다.
- 이 프로젝트에만 맞으면 `scope=project`, 어디서나 맞으면 `scope=global`.
- 제목은 한 줄로, 본문은 짧게. 대화 원문 전체, 비밀값·토큰·비밀번호는 남기지 않는다.
- 같은 내용이 이미 있으면 새로 만들지 말고 그 기억을 고친다.

## 고치기

- 틀렸거나 바뀐 기억은 `memory_update`로 고친다. 읽은 판본을 `expectedVersion`에 넣는다.
- 더 맞지 않으면 `action=stale`, 새 기억이 대신하면 `action=supersede`와 `supersededBy`.
- 저장·수정 결과의 id와 상태를 사용자에게 짧게 알린다. 실패하면 실패라고 말한다.
