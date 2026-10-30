// 플러그인이 보이는 글의 언어(ONID-61). Studio의 언어(ctx.locale)를 따르고 기본은 영어다.
// 글은 영역마다 messages/<영역>.ts에 en·ko 사전으로 둔다. ko는 en과 같은 모양이어야 한다(타입이 확인).
// 사용자가 쓴 내용(기억·제안 본문, 작업 제목)과 oni가 돌려준 데이터는 번역하지 않는다.
import {useSyncExternalStore} from 'react';
import type {StudioLocale} from '@onidot-labs/studio-plugin';

export type Locale = StudioLocale;

let current: Locale = 'en';
const listeners = new Set<() => void>();

export function setLocale(locale: Locale): void {
  const next: Locale = locale === 'ko' ? 'ko' : 'en';
  if (next === current) return;
  current = next;
  listeners.forEach(l => l());
}

export function currentLocale(): Locale {
  return current;
}

export function onLocale(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** 지금 언어. 언어가 바뀌면 이 훅을 부른 화면을 다시 그린다. */
export function useLocale(): Locale {
  return useSyncExternalStore(onLocale, currentLocale, currentLocale);
}

/** Intl에 넘길 언어 태그. */
export function localeTag(locale: Locale = current): string {
  return locale === 'ko' ? 'ko-KR' : 'en-US';
}

/** 영역 사전: en이 모양의 기준이고 ko는 같은 키·같은 함수 인자를 가진다. */
export interface Catalog<T> {
  en: T;
  ko: NoInfer<T>;
}

export function defineMessages<T>(catalog: Catalog<T>): Catalog<T> {
  return catalog;
}

/** 지금 언어의 사전. 모듈 최상위 상수에 담지 말고 그릴 때·부를 때 읽는다. */
export function pick<T>(catalog: Catalog<T>, locale: Locale = current): T {
  return catalog[locale];
}

/** 화면용: 언어가 바뀌면 다시 그리고 그 언어의 사전을 준다. */
export function useMessages<T>(catalog: Catalog<T>): T {
  return catalog[useLocale()];
}

/** activate에서 Studio 언어를 따르게 한다. 돌려받은 함수로 끊는다. */
export function followStudio(locale: {current(): Locale; onChange(handler: (l: Locale) => void): () => void} | undefined): () => void {
  if (!locale) {
    setLocale('en');
    return () => {};
  }
  setLocale(locale.current());
  return locale.onChange(setLocale);
}
