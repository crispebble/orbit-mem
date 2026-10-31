---
name: ulw-execute
description: "Use when a plan written by ulw-plan, or orbit-pi work that stopped (closed terminal, limit reached, question answered, crash), should run on from its orbit.work record instead of starting over. / ulw-plan으로 쓴 계획이나 멈춘 orbit-pi 작업을 처음부터 다시 하지 않고 orbit.work 기록에서 이어 실행할 때 쓴다. Triggers (case-insensitive): ulw-execute, resume, continue the plan, 이어서 해, 이어서, 재개"
---

# ulw-execute

Carry out a recorded plan, or continue orbit-pi work that stopped, from what orbit.work holds: the same work and steps, finished steps kept and their evidence intact. Nothing is planned again unless the record shows the plan was never finished.

## Find the work

List the open orbit-pi work for this folder:

```sh
orb work list --cwd . --state open --launcher orbit-pi
```

Keep only items whose `origin.host` is `orbit-pi` (the orchestrator's own work, list kind `orbit-pi-plan`). If one is left, use it. If several, show their titles and ask which one. If none, there is nothing to continue; use `orbit:ultrawork` or `orbit:ulw-plan`.

To tell the person where it stands:

```sh
orb work get <work id>
```

Look at steps that are `in_progress`, `blocked` (with `block.reason`) or `ready`.

## Run

```sh
orb run --resume <work id>
```

Without an id, `orb run --resume` takes the most recent open orbit-pi work in this folder. The flags of `orbit:ultrawork` apply (`--max-cost`, `--max-turns`, `--max-minutes`, `--json`, `--events`). Exit code `0` is done, `1` failed, `3` stopped at a limit or waiting for an answer. Report as `orbit:ultrawork` describes.

## What not to do

- Do not start a new `orb run` with the same request; it would make a second work.
- Do not mark steps done by hand.
- Do not merge, push, open a PR or deploy.

## 한국어 요약

계획이나 멈춘 orbit-pi 작업은 orbit.work 기록에서 이어 실행한다. `orb work list --cwd . --state open --launcher orbit-pi`에서 `origin.host`가 `orbit-pi`인 작업을 찾고(여럿이면 사용자에게 고르게 함), `orb work get <작업 id>`로 어디서 멈췄는지 본 뒤 `orb run --resume <작업 id>`(id를 빼면 이 폴더의 가장 최근 작업)로 잇는다. 같은 요청으로 새 `orb run`을 하지 않는다. 병합·푸시는 하지 않는다.
