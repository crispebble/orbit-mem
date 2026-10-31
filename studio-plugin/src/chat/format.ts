// 숫자·글 줄이기. 화면 언어(ko-KR/en-US)로 찍는다.
import {localeTag} from '../i18n/index.ts';

export function formatUsd(n: number, locale = localeTag()): string {
  const digits = n !== 0 && Math.abs(n) < 0.01 ? 4 : 2;
  return new Intl.NumberFormat(locale, {style: 'currency', currency: 'USD', minimumFractionDigits: digits, maximumFractionDigits: digits}).format(n);
}

export function formatCount(n: number, locale = localeTag()): string {
  return new Intl.NumberFormat(locale).format(n);
}

export function formatMinutes(n: number, locale = localeTag()): string {
  return new Intl.NumberFormat(locale, {maximumFractionDigits: 1}).format(n);
}

/** 한 줄로 줄인다(목록·선택지용). */
export function oneLine(text: string, max = 48): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

/** 마지막 n줄(검증 출력 끝줄). */
export function tailLines(text: string, n = 12): string {
  const lines = text.replace(/\s+$/, '').split('\n');
  return lines.slice(-n).join('\n');
}

export function shortSha(sha: string): string {
  return sha.slice(0, 8);
}
