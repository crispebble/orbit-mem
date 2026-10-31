---
name: ultrawork
description: "Use when the person wants a multi-step request taken all the way to done by the orbit-pi orchestrator without stopping for plan approval: plan, run steps in parallel, verify, finish. Not for planning only (that is ulw-plan). / 계획 승인 없이 orbit-pi 오케스트레이터가 계획→병렬 실행→검증을 끝까지 해내게 할 때 쓴다. 계획만 쓰는 일은 ulw-plan이다. Triggers (case-insensitive): ulw, ultrawork, all the way, 끝까지, 끝까지 해"
---

# ultrawork

Hand a multi-step request to the orbit-pi orchestrator and let it run to the end without asking for plan approval first. The orchestrator plans the work as steps with predecessors and done criteria, runs the steps with no unfinished predecessor in parallel (each role on its own model), verifies with test commands and a reviewer verdict, fixes what failed, and finishes. Plan, progress, evidence and cost go to orbit.work, so you read the result there instead of guessing.

## When to use

- The person says ulw, ultrawork, all the way, or 끝까지 (해), or asks to finish without checking in.
- The request has several steps that can be planned, split and checked, and is clear enough that the plan does not need review.

Use something else when:

- The person wants to see the plan first, or the request is vague or hard to undo: use `orbit:ulw-plan`, then `orbit:ulw-execute`. "ulw plan" and "ulw-plan" mean planning only, not this skill.
- The work stopped earlier: use `orbit:ulw-execute` to continue it.
- It is a one-file edit or a question: do it in this conversation.

## How to run

From Bash, in the repository the work is for:

```sh
orb run --ultrawork "<request in the person's words>"
```

| Flag | Meaning |
| --- | --- |
| `--max-cost USD` | Stop before spending more than this (subscription engines count tokens only) |
| `--max-turns N` | Stop after this many agent turns in total |
| `--max-minutes N` | Stop after this much wall time |
| `--json` | Print one JSON summary at the end (work id, state, steps, cost) |
| `--events` | Stream progress events as JSON lines while it runs |

Prefer `--json` when you will read the result yourself. Set limits when the person gave a budget; do not raise limits the person set.

## Stop rules

Running to the end does not mean it never stops:

- When it needs the person (a choice, a permission, missing information), it asks only through a decision card and waits.
- It stops at a cost, turn or time limit.
- It stops when the same failure happens three times in a row.

## Reading the result

| exit code | Meaning | What you do |
| --- | --- | --- |
| `0` | Done: every step finished and verification passed | Report the summary |
| `1` | Failed: a step failed or verification did not pass | Report which step and why, from the evidence |
| `3` | Stopped at a limit, or waiting for an answer | Say which limit or question; continue with `orbit:ulw-execute` once the person answers or raises the limit |

Then read the record:

```sh
orb work get <work id>
```

The JSON (orbit.work contract) has `steps` (status, `after` predecessors, `detail`, `verification`), `runs` (role, step, `cost`) and `evidence` (test commands, commits, verdicts). Report in the person's language:

1. One line: done, failed or stopped, and why.
2. Each step: title, status, verification result.
3. The evidence behind it: test command lines and commits.
4. Cost and turns when the runs give them.
5. What is left.

Quote the record. Do not say a step passed when its verification is `none` or `fail`.

## What not to do

- Do not merge, push, open a PR or deploy. The person decides.
- Do not answer the orchestrator's decision cards on the person's behalf.
- Do not rerun the whole request after a stop; continue it with `orbit:ulw-execute`.
- Do not edit the orbit.work record by hand.

## 한국어 요약

여러 단계 일을 계획 승인 없이 끝까지 맡길 때 `orb run --ultrawork "<요청>"`(Bash)을 쓴다. 오케스트레이터가 계획(단계·선행·완료 기준) → 선행 없는 단계 병렬 실행(역할별 모델) → 검증 명령·reviewer 판정까지 한다. 사용자가 필요하면 결정 카드로만 묻고, 비용·턴·시간 상한(`--max-cost`·`--max-turns`·`--max-minutes`)에 닿거나 같은 실패가 세 번 나면 멈춘다. 종료 코드 0은 완료, 1은 실패, 3은 멈춤이다. `orb work get <작업 id>`로 단계·증거·비용을 읽어 보고한다. 계획만 원하면 `orbit:ulw-plan`, 멈춘 일은 `orbit:ulw-execute`. 병합·푸시는 하지 않는다.
