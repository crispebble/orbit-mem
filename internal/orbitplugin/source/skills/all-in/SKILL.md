---
name: all-in
description: "Use when the person wants orbit-pi to take a request all the way to done without stopping for plan approval (all-in mode, like ultrawork). / 계획 승인 없이 orbit-pi가 끝까지 해내게 할 때 쓴다(all-in 모드). Triggers: all-in, allin, ulw, ultrawork, 끝까지, 끝까지 해, 알아서 끝까지"
---

# all-in

All-in mode runs a request through the orbit-pi orchestrator to the end without asking for plan approval first. It is OpenOrbit's place for ultrawork: plan, run the steps in parallel, verify, fix, and keep going until done or until a stop rule fires.

## When to use

- The person says all-in, allin, ulw, or 끝까지 (해), or asks to finish without checking in.
- The request is clear enough that the plan does not need the person's review.

If the request is vague or touches something hard to undo, use `orbit:run` instead so the plan is approved first.

## How to run

```sh
orb run --all-in "<request>"
```

A request that already contains the keyword `all-in`, `allin` or `끝까지` also starts all-in mode with plain `orb run`. The limits of `orbit:run` apply: `--max-cost`, `--max-turns`, `--max-minutes`, and `--json` or `--events` for the output.

## Stop rules

All-in does not mean it never stops:

- When it needs the person (a choice, a permission, missing information), it asks only through a decision card and waits. It does not stop to chat.
- It stops when it reaches a cost, turn or time limit.
- It stops when the same failure happens three times in a row.

A stop ends with exit code `3`. Read the work with `orb work get <work id>`, tell the person which rule fired and what is needed, and continue with `orbit:resume` once they answer or raise the limit.

## Reporting

Report as in `orbit:run`: outcome in one line, each step with its verification, the evidence (test commands, commits), cost, and what is left. Exit code `0` is done, `1` failed, `3` stopped.

## What not to do

- Do not merge, push, open a PR or deploy, even in all-in mode.
- Do not answer the orchestrator's decision cards on the person's behalf.
- Do not raise limits the person set.

## 한국어 요약

all-in 모드는 계획 승인 없이 끝까지 간다(OmO의 ultrawork 자리). `orb run --all-in "<요청>"`으로 부르거나, 요청에 `all-in`·`allin`·`끝까지`가 있으면 `orb run`만으로도 켜진다. 사용자가 필요하면 결정 카드로만 묻고 기다린다. 비용·턴·시간 상한에 닿거나 같은 실패가 세 번 나면 멈춘다(종료 코드 3). 멈추면 `orb work get`으로 이유를 읽어 알리고, 답을 받으면 `orbit:resume`으로 잇는다. 병합·푸시는 하지 않는다.
