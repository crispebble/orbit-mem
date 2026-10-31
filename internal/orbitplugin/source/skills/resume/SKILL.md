---
name: resume
description: "Use when orbit-pi work stopped (closed terminal, limit reached, a question answered, a crash) and should continue from its orbit.work record instead of starting over. / 멈췄던 orbit-pi 작업을 처음부터 다시 하지 않고 orbit.work 기록에서 이어서 할 때 쓴다. Triggers: resume, continue orbit-pi, pick up where it left off, 이어서, 이어서 해, 다시 이어, 재개"
---

# resume

Continue orbit-pi work that stopped, from what orbit.work recorded: the same work, the same steps, with finished steps kept and verified evidence intact. Nothing is planned again unless the record says the plan was never finished.

## Find the work

List the open orbit-pi work for this folder:

```sh
orb work list --cwd . --state open --launcher orbit-pi
```

Each item has the work `id`, `title`, step counts and verification counts. If there is exactly one, use it. If there are several, show the titles to the person and ask which one. If there is none, there is nothing to resume; use `orbit:run`.

Read it before resuming when you need to tell the person where it stopped:

```sh
orb work get <work id>
```

Look at the steps that are `in_progress`, `blocked` (and their `block.reason`) or `ready`.

## Resume

```sh
orb run --resume <work id>
```

Without an id, `orb run --resume` picks the most recent open orbit-pi work in this folder. The limits and output flags of `orbit:run` apply (`--max-cost`, `--max-turns`, `--max-minutes`, `--json`, `--events`), and so does `--all-in` if the person wants it to go to the end.

Exit code `0` is done, `1` failed, `3` stopped at a limit or waiting for an answer. Report as in `orbit:run`.

## What not to do

- Do not start a new `orb run` with the same request; it would make a second work.
- Do not merge, push, open a PR or deploy.
- Do not mark steps done by hand.

## 한국어 요약

멈춘 orbit-pi 작업은 orbit.work 기록에서 이어서 한다. `orb work list --cwd . --state open --launcher orbit-pi`로 열린 작업을 찾고(여럿이면 사용자에게 고르게 함), `orb work get <작업 id>`로 어디서 멈췄는지 본 뒤 `orb run --resume <작업 id>`(id를 빼면 이 폴더의 가장 최근 작업)로 잇는다. 같은 요청으로 새 `orb run`을 하지 않는다. 병합·푸시는 하지 않는다.
