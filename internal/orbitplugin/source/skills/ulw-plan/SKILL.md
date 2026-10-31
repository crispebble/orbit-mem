---
name: ulw-plan
description: "Use when the person wants a plan first: orbit-pi reads the code and writes a plan (steps, predecessors, done criteria) to orbit.work without changing anything. / 바꾸기 전에 orbit-pi가 코드를 살피고 계획(단계·선행·완료 기준)만 써서 orbit.work에 남기게 할 때 쓴다. Triggers (case-insensitive): ulw plan, ulw-plan, plan first, plan only, 계획만, 계획 먼저"
---

# ulw-plan

Have the orbit-pi orchestrator study the code and write a plan, and stop there. The plan is recorded in orbit.work as steps with predecessors, done criteria and details, so the person can review it and `orbit:ulw-execute` can carry it out later. This skill does not change files, run builds that write, or commit.

## When to use

- The person says ulw plan, ulw-plan, plan first, or 계획만.
- The request is large, vague or hard to undo, and the person should see the plan before work starts.

If the person wants it done without review, use `orbit:ultrawork`.

## How to run

```sh
orb run --plan "<request in the person's words>"
```

`--json` prints the work id and the plan at the end; `--max-cost`, `--max-turns` and `--max-minutes` limit the planning itself. Exit code `0` means the plan was written, `1` that planning failed, `3` that it stopped at a limit or needs an answer.

## Show the plan

```sh
orb work get <work id>
```

Show the person, in their language: the work title, each step in order with its title, `detail` and predecessors (`after`), which steps can run in parallel (same `wave`), and the done criteria. Ask whether to run it as is, change it, or drop it.

To run it, use `orbit:ulw-execute` with this work id.

## What not to do

- Do not change code or files while planning; the orchestrator only reads.
- Do not start execution without the person's go-ahead.
- Do not merge, push, open a PR or deploy.

## 한국어 요약

먼저 계획만 원할 때 `orb run --plan "<요청>"`을 쓴다. orbit-pi가 코드를 읽고 계획(단계·선행·완료 기준·설명)을 orbit.work에 남기며 아무것도 바꾸지 않는다. `orb work get <작업 id>`로 계획을 읽어 단계 순서·병렬 묶음(wave)·완료 기준을 보여 주고, 그대로 실행할지 묻는다. 실행은 `orbit:ulw-execute`로 한다. 병합·푸시는 하지 않는다.
