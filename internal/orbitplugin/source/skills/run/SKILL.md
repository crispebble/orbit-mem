---
name: run
description: "Use when a request needs several steps (plan, parallel work, verification) and should go to the orbit-pi orchestrator instead of being done turn by turn in this conversation. / 여러 단계가 필요한 일(계획·병렬 작업·검증)을 orbit-pi 오케스트레이터에 맡길 때 쓴다. Triggers: orb run, orbit run, orchestrate, 오케스트레이터, 맡겨, 계획하고 실행"
---

# run

Hand a multi-step request to the orbit-pi orchestrator and report what it did. The orchestrator plans the work as steps with predecessors and done criteria, runs the steps that have no unfinished predecessor in parallel (each role on its own model), verifies with test commands and a reviewer verdict, and finishes. Its plan, progress, evidence and cost are recorded in orbit.work, so you read the result there instead of guessing.

## When to use

- The request has several steps that can be planned, split and checked: a feature across modules, a refactor with tests, a batch of fixes.
- The person asks to run it with orbit-pi, to orchestrate, or to plan and execute.

Do not use it for a one-file edit or a question. Do those in this conversation.

## How to run

Call it from Bash in the repository the work is for:

```sh
orb run "<request in the person's words>"
```

Useful flags:

| Flag | Meaning |
| --- | --- |
| `--max-cost USD` | Stop before spending more than this (subscription engines count tokens only) |
| `--max-turns N` | Stop after this many agent turns in total |
| `--max-minutes N` | Stop after this much wall time |
| `--json` | Print one JSON summary at the end (work id, state, steps, cost) |
| `--events` | Stream progress events as JSON lines while it runs |

Prefer `--json` when you will read the result yourself. Set limits when the person gave a budget; otherwise leave the orchestrator's defaults.

## Reading the result

The exit code says how it ended:

| exit code | Meaning | What you do |
| --- | --- | --- |
| `0` | Done: every step finished and verification passed | Report the summary |
| `1` | Failed: a step failed or verification did not pass | Report which step and why, from the evidence |
| `3` | Stopped at a limit, or it needs an answer from the person | Say which limit or question; offer `orbit:resume` after the person answers or raises the limit |

Then read the recorded plan and evidence:

```sh
orb work get <work id>
```

The JSON (orbit.work contract) has `steps` (status, `after` predecessors, `detail`, `verification`), `runs` (role, step, `cost`) and `evidence` (test commands, commits, verdicts). Report to the person, in their language:

1. One line: done, failed or stopped, and why.
2. Each step: title, status, verification result.
3. Evidence that backs it: the test command lines and commits.
4. Cost and turns when the runs give them.
5. What is left, if anything.

Quote what the record says. Do not claim a step passed when its verification is `none` or `fail`.

## What not to do

- Do not merge, push, open a PR or deploy. The orchestrator does not either; the person decides.
- Do not rerun the whole request after a failure. Use `orbit:resume` to continue the same work.
- Do not edit the orbit.work record by hand.

## 한국어 요약

여러 단계 일은 `orb run "<요청>"`(Bash)으로 orbit-pi에 맡긴다. 오케스트레이터가 계획(단계·선행·완료 기준) → 선행 없는 단계 병렬 실행(역할별 모델) → 검증 명령·reviewer 판정 → 완료까지 한다. 종료 코드 0은 완료, 1은 실패, 3은 상한에 닿았거나 사용자 답이 필요함이다. `orb work get <작업 id>`로 단계·증거·비용을 읽어 사용자 언어로 보고한다. 상한은 `--max-cost`·`--max-turns`·`--max-minutes`, 결과 읽기는 `--json`·`--events`. 병합·푸시는 하지 않는다.
