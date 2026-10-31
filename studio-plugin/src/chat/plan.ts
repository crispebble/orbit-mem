// 계획의 병렬 묶음: 선행(after)이 없는 단계가 묶음 1, 그 묶음만 기다리면 묶음 2 …
// 묶음 번호 = 선행 깊이(가장 긴 선행 사슬의 길이). 같은 묶음의 단계는 서로 기다리지 않으므로 병렬로 돈다.
export interface PlanStepLike {
  id: string;
  after: readonly string[];
}

/** 단계 id를 묶음 순서대로, 묶음 안에서는 계획에 적힌 순서대로. 모르는 선행 id는 무시하고 순환은 끊는다. */
export function planBatches(steps: readonly PlanStepLike[]): string[][] {
  const byId = new Map(steps.map(s => [s.id, s] as const));
  const depth = new Map<string, number>();
  const visiting = new Set<string>();
  const depthOf = (id: string): number => {
    const known = depth.get(id);
    if (known !== undefined) return known;
    // 순환: 이 단계는 선행으로 치지 않는다
    if (visiting.has(id)) return 0;
    visiting.add(id);
    let d = 0;
    for (const a of byId.get(id)?.after ?? []) if (byId.has(a) && a !== id) d = Math.max(d, depthOf(a) + 1);
    visiting.delete(id);
    depth.set(id, d);
    return d;
  };
  const batches: string[][] = [];
  for (const s of steps) {
    const d = depthOf(s.id);
    (batches[d] ??= []).push(s.id);
  }
  return batches.filter(Boolean);
}
