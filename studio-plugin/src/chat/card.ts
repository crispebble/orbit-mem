// 결정 카드의 고르기 모델(Needs you 카드와 같은 동작): 선택지는 하나만 고르고, 「다른 답」을 쓰면 그 글이 답이다.
export interface CardPicks {
  choice?: string;
  text: string;
}

export const emptyPicks: CardPicks = {text: ''};

/** 같은 선택지를 다시 누르면 고름이 풀린다. 고르면 직접 쓴 글은 비운다(답이 둘이 되지 않게). */
export function pickChoice(picks: CardPicks, id: string): CardPicks {
  return picks.choice === id ? {text: picks.text} : {choice: id, text: ''};
}

/** 글을 쓰기 시작하면 고름은 풀린다. */
export function writeText(picks: CardPicks, text: string): CardPicks {
  return text.trim() === '' && picks.choice ? {...picks, text} : {text};
}

/** 보낼 답. 직접 쓴 글이 있으면 그것, 없으면 고른 선택지, 둘 다 없으면 보낼 수 없다. */
export function answerFor(picks: CardPicks): string | undefined {
  const text = picks.text.trim();
  if (text) return text;
  return picks.choice;
}

/** 숫자 키 1-9로 고른다. 선택지 밖의 키는 그대로 돌려준다(같은 객체). */
export function pickByDigit(options: readonly string[], picks: CardPicks, key: string): CardPicks {
  if (!/^[1-9]$/.test(key)) return picks;
  const id = options[Number(key) - 1];
  return id === undefined ? picks : pickChoice(picks, id);
}
