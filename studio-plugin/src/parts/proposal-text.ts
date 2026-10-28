// ProposalDetail이 쓰는 순수 함수.

/** 원래 글과 고친 글이 몇 줄 다른지 센다. 줄 수 차이도 더한다. */
export function changedLines(original: string, edited: string): number {
  const a = original.split('\n');
  const b = edited.split('\n');
  let count = Math.abs(a.length - b.length);
  for (let index = 0; index < Math.min(a.length, b.length); index++) {
    if (a[index] !== b[index]) count++;
  }
  return count;
}
