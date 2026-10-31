// 프로필 편집기의 모델: 사슬 편집, 검증, 저장 페이로드, 키 판정. 순수 함수만 있다.
// 형식의 기준은 orbit-profiles(types.ts·file.ts). 여기 검증은 저장 전에 빠르게 알려 주는 것이고, 최종 판정은 데몬이다.
import {errorParts} from '../chat/errors.ts';
import {requiredKey, type DaemonApi, type ProfileFileJson, type ProfileJson, type ProfilesView} from '../daemon/api.ts';

export type Chain = string | string[] | undefined;
export type RunSource = 'terminal' | 'studio' | 'workflow' | 'schedule';
export type RunRule = 'subscription' | 'api' | 'subscription-first' | 'api-first';
export const RUN_SOURCES: readonly RunSource[] = ['terminal', 'studio', 'workflow', 'schedule'];
export const RUN_RULES: readonly RunRule[] = ['subscription', 'api', 'subscription-first', 'api-first'];
/** 지금 쓰는 역할 키. 새 키도 더할 수 있다. */
export const ROLE_NAMES = ['default', 'planner', 'worker', 'reviewer', 'qa', 'explorer'] as const;

export function chainOf(value: Chain): string[] {
  return value === undefined ? [] : Array.isArray(value) ? [...value] : [value];
}

/** 하나면 문자열, 여럿이면 배열, 없으면 undefined(형식 그대로 저장). */
export function chainValue(list: readonly string[]): Chain {
  return list.length === 0 ? undefined : list.length === 1 ? list[0] : [...list];
}

/** 위(-1)·아래(+1)로 한 칸. 끝에서는 그대로(같은 배열). */
export function moveRef(list: readonly string[], index: number, dir: -1 | 1): string[] {
  const to = index + dir;
  if (index < 0 || index >= list.length || to < 0 || to >= list.length) return list as string[];
  const next = [...list];
  [next[index], next[to]] = [next[to], next[index]];
  return next;
}

/** 빈 글·이미 있는 값은 더하지 않는다(같은 배열). */
export function addRef(list: readonly string[], ref: string): string[] {
  const r = ref.trim();
  return r === '' || list.includes(r) ? (list as string[]) : [...list, r];
}

export function removeRef(list: readonly string[], index: number): string[] {
  return list.filter((_, i) => i !== index);
}

export function setRole(profile: ProfileJson, role: string, list: readonly string[]): ProfileJson {
  const roles = {...profile.roles};
  const v = chainValue(list);
  if (v === undefined) delete roles[role];
  else roles[role] = v;
  return {...profile, roles};
}

export function setCheap(profile: ProfileJson, list: readonly string[]): ProfileJson {
  const next = {...profile};
  const v = chainValue(list);
  if (v === undefined) delete next.cheap;
  else next.cheap = v;
  return next;
}

/** `engine:*`, 끝 `*`(`pi:openrouter/*`), 정확히 같은 값. */
export function isAllowedRef(allowed: readonly string[], ref: string): boolean {
  return allowed.some(a => a === ref || (a.endsWith('*') && ref.startsWith(a.slice(0, -1))));
}

// ----- 검증 -----

export type IssueCode = 'name' | 'absolute' | 'notAllowed' | 'positive' | 'duplicate' | 'empty';
export interface Issue {
  /** 데몬이 쓰는 칸 이름에서 `profiles[i].`를 뺀 것. 예: `roles.planner`, `limits.turns`, `folders[1]` */
  field: string;
  code: IssueCode;
  /** notAllowed의 모델 */
  ref?: string;
}

const isAbs = (p: string) => p.startsWith('/') || /^[A-Za-z]:[\\/]/.test(p);
const positive = (n: unknown) => typeof n === 'number' && Number.isFinite(n) && n > 0;

export function validateProfile(profile: ProfileJson, others: readonly string[] = []): Issue[] {
  const out: Issue[] = [];
  if (profile.name.trim() === '') out.push({field: 'name', code: 'name'});
  else if (others.includes(profile.name)) out.push({field: 'name', code: 'duplicate'});
  profile.folders.forEach((f, i) => {
    if (!isAbs(f)) out.push({field: `folders[${i}]`, code: 'absolute'});
  });
  for (const key of ['claudeConfigDir', 'codexHome'] as const) {
    const v = profile[key];
    if (v !== undefined && v !== '' && !isAbs(v)) out.push({field: key, code: 'absolute'});
  }
  const chains: [string, string[]][] = [...Object.entries(profile.roles).map(([r, v]) => [`roles.${r}`, chainOf(v)] as [string, string[]]), ['cheap', chainOf(profile.cheap)]];
  for (const [field, list] of chains) {
    list.forEach((ref, i) => {
      if (!isAllowedRef(profile.allowedModels, ref)) out.push({field: list.length > 1 ? `${field}[${i}]` : field, code: 'notAllowed', ref});
    });
  }
  for (const key of ['turns', 'costUsd', 'minutes'] as const) {
    if (!positive(profile.limits[key])) out.push({field: `limits.${key}`, code: 'positive'});
  }
  for (const key of ['monthlyLimitUsd', 'monthlyApiLimitUsd'] as const) {
    const v = profile[key];
    if (v !== undefined && !positive(v)) out.push({field: key, code: 'positive'});
  }
  profile.folderRules?.forEach((r, i) => {
    if (r.models.length === 0) out.push({field: `folderRules[${i}].models`, code: 'empty'});
    for (const k of ['allow', 'deny'] as const) {
      (r[k] ?? []).forEach((p, j) => {
        if (!isAbs(p)) out.push({field: `folderRules[${i}].${k}[${j}]`, code: 'absolute'});
      });
    }
  });
  return out;
}

/** 데몬의 RPC_INVALID_ARGUMENT 메시지(`profiles[0].roles.planner: …`)에서 칸 이름과 글을 뗀다. 칸이 없으면 field는 빈 글. */
export function parseFieldError(message: string): {field: string; text: string} {
  const m = /^\s*(?:\[[a-z_]+\]\s*)?((?:profiles|default|version)[\w.[\]-]*)\s*:\s*(.*)$/s.exec(message);
  if (!m) return {field: '', text: message};
  return {field: m[1].replace(/^profiles\[\d+\]\.?/, ''), text: m[2]};
}

/** 오류 칸(error)이 이 칸(field) 또는 그 아래 칸인가. */
export function fieldWithin(errorField: string, field: string): boolean {
  return errorField === field || errorField.startsWith(`${field}.`) || errorField.startsWith(`${field}[`);
}

// ----- 저장 페이로드 -----

const nonEmpty = (list: readonly string[] | undefined) => (list ?? []).map(s => s.trim()).filter(Boolean);

/** 빈 칸을 걷어 낸 프로필(데몬은 빈 문자열·빈 목록을 "있음"으로 읽을 수 있어 아예 뺀다). 모르는 칸은 그대로 둔다. */
export function cleanProfile(profile: ProfileJson): ProfileJson {
  const p: ProfileJson = {
    ...profile,
    name: profile.name.trim(),
    folders: nonEmpty(profile.folders),
    apiKeys: nonEmpty(profile.apiKeys),
    allowedModels: nonEmpty(profile.allowedModels),
    roles: {},
    keywords: {ultrawork: nonEmpty(profile.keywords.ultrawork), plan: nonEmpty(profile.keywords.plan)},
  };
  for (const [role, v] of Object.entries(profile.roles)) {
    const list = nonEmpty(chainOf(v));
    const value = chainValue(list);
    if (role.trim() && value !== undefined) p.roles[role.trim()] = value;
  }
  for (const key of ['claudeConfigDir', 'codexHome'] as const) {
    if ((p[key] ?? '').trim() === '') delete p[key];
    else p[key] = p[key]!.trim();
  }
  const cheap = chainValue(nonEmpty(chainOf(profile.cheap)));
  if (cheap === undefined) delete p.cheap;
  else p.cheap = cheap;
  for (const key of ['monthlyLimitUsd', 'monthlyApiLimitUsd'] as const) {
    if (p[key] === undefined || p[key] === null) delete p[key];
  }
  if (p.runRules) {
    const rules = Object.fromEntries(Object.entries(p.runRules).filter(([, v]) => v !== undefined && v !== ('' as string)));
    if (Object.keys(rules).length === 0) delete p.runRules;
    else p.runRules = rules;
  }
  if (p.folderRules) {
    const rules = p.folderRules
      .map(r => {
        const next: {models: string[]; allow?: string[]; deny?: string[]} = {models: nonEmpty(r.models)};
        const allow = nonEmpty(r.allow);
        const deny = nonEmpty(r.deny);
        if (allow.length) next.allow = allow;
        if (deny.length) next.deny = deny;
        return next;
      })
      .filter(r => r.models.length > 0);
    if (rules.length === 0) delete p.folderRules;
    else p.folderRules = rules;
  }
  return p;
}

/** 저장할 파일: 같은 이름(originalName)의 프로필을 편집본으로 바꾼다(이름을 바꾸면 default도 따라간다). */
export function toSaveFile(file: ProfileFileJson, originalName: string, draft: ProfileJson): ProfileFileJson {
  const cleaned = cleanProfile(draft);
  // apiKeys는 키 넣기·지우기로 데몬이 바꾼다: 편집 대상이 아니므로 항상 최신 파일의 값을 쓴다
  const current = file.profiles.find(p => p.name === originalName);
  if (current) cleaned.apiKeys = [...(current.apiKeys ?? [])];
  const next: ProfileFileJson = {...file, profiles: file.profiles.map(p => (p.name === originalName ? cleaned : p))};
  if (file.default === originalName) next.default = cleaned.name;
  return next;
}

export function newProfile(name: string): ProfileJson {
  return {
    name: name.trim(),
    folders: [],
    apiKeys: [],
    allowedModels: ['claude:*'],
    roles: {default: 'claude:haiku', planner: 'claude:haiku', reviewer: 'claude:haiku', explorer: 'claude:haiku', qa: 'claude:haiku'},
    cheap: 'claude:haiku',
    limits: {turns: 60, costUsd: 5, minutes: 60},
    keywords: {ultrawork: ['ulw', 'ultrawork'], plan: ['ulw plan']},
  };
}

export function addProfile(file: ProfileFileJson, name: string): ProfileFileJson {
  return {...file, profiles: [...file.profiles, newProfile(name)]};
}

/** 마지막 하나는 지우지 않는다(같은 파일). 기본이던 것을 지우면 default를 비운다. */
export function removeProfile(file: ProfileFileJson, name: string): ProfileFileJson {
  if (file.profiles.length <= 1) return file;
  const next: ProfileFileJson = {...file, profiles: file.profiles.filter(p => p.name !== name)};
  if (file.default === name) delete next.default;
  return next;
}

export function setDefaultProfile(file: ProfileFileJson, name: string): ProfileFileJson {
  return {...file, default: name};
}

/** default가 비면 첫 프로필이 기본이다(orbit-profiles와 같은 규칙). */
export function defaultName(file: ProfileFileJson): string | undefined {
  return file.default ?? file.profiles[0]?.name;
}

// ----- 키 (P3·P4) -----

/** 이 프로필이 가진(set) 공급처. */
export function keysSet(keys: {provider: string; set: boolean}[] | undefined): Set<string> {
  return new Set((keys ?? []).filter(k => k.set).map(k => k.provider));
}

/** 키가 필요한 공급처: 사슬·cheap·허용 모델 목록·apiKeys에 이름이 나온 것. */
export function neededProviders(profile: ProfileJson, keys?: {provider: string}[]): string[] {
  const out = new Set<string>(profile.apiKeys.filter(Boolean));
  const refs = [...Object.values(profile.roles).flatMap(chainOf), ...chainOf(profile.cheap), ...profile.allowedModels];
  for (const ref of refs) {
    const k = requiredKey(ref);
    if (k) out.add(k);
  }
  for (const k of keys ?? []) out.add(k.provider);
  return [...out].sort();
}

/** P4: 모델에 필요한 공급처 키가 없으면 true(「키 없음 — 건너뜀」을 붙인다). 키가 필요 없는 모델은 false. */
export function lacksKey(ref: string, have: ReadonlySet<string>): boolean {
  const k = requiredKey(ref);
  return k !== undefined && !have.has(k);
}

/** 사슬 안에서 건너뛰는 모델 위치. */
export function skippedInChain(list: readonly string[], have: ReadonlySet<string>): number[] {
  return list.flatMap((ref, i) => (lacksKey(ref, have) ? [i] : []));
}

// ----- 사용량 (P1·P5) -----

export function usageRatio(spent: number, limit: number): number | undefined {
  return limit > 0 ? spent / limit : undefined;
}

// ----- 저장 실패 -----

export type SaveProblem = {kind: 'changed'} | {kind: 'field'; field: string; text: string} | {kind: 'other'; text: string};

/** 저장 실패 → 화면에 보일 종류. 칸 이름이 있는 RPC_INVALID_ARGUMENT는 그 칸 근처에, RPC_ABORTED는 「다시 불러오기」. */
export function classifySaveError(error: unknown, fallback: string): SaveProblem {
  const p = errorParts(error);
  if (p?.code === 'RPC_ABORTED') return {kind: 'changed'};
  if (p?.code === 'RPC_INVALID_ARGUMENT') {
    const f = parseFieldError(p.message);
    return f.field ? {kind: 'field', field: f.field, text: f.text} : {kind: 'other', text: f.text};
  }
  return {kind: 'other', text: fallback};
}

// ----- 이름 바꾸기·삭제와 키 -----

/** 저장된 키(set)가 있는 프로필은 이름을 바꿀 수 없다(키 계정이 `<프로필>:<공급처>`라 키를 잃고 옛 키가 남는다). */
export function renameBlocked(keys: readonly {set: boolean}[] | undefined, savedName: string, draftName: string): boolean {
  return draftName.trim() !== savedName && (keys ?? []).some(k => k.set);
}

/** 삭제: 저장된 키를 먼저 모두 지운 다음, 최신 파일·revision으로 프로필을 뺀다. */
export async function deleteProfileWithKeys(
  api: Pick<DaemonApi, 'deleteApiKey' | 'getProfiles' | 'saveProfiles'>,
  name: string,
  keys: readonly {provider: string; set: boolean}[] | undefined,
): Promise<void> {
  for (const k of keys ?? []) if (k.set) await api.deleteApiKey({profile: name, provider: k.provider});
  const fresh = normalizeView(await api.getProfiles());
  await api.saveProfiles({file: removeProfile(fresh.file, name), expectedRevision: fresh.revision});
}

// ----- 빠진 칸 채우기 -----

/** 데몬은 깨진 파일의 원문도 돌려줄 수 있다: 필수 칸이 빠져도 편집기가 깨지지 않게 기본값으로 채운다. */
export function normalizeProfile(raw: Partial<ProfileJson> & {name?: string}): ProfileJson {
  const d = newProfile(raw.name ?? '');
  const list = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
  const lim = (raw.limits ?? {}) as Partial<ProfileJson['limits']>;
  const kw = (raw.keywords ?? {}) as Partial<ProfileJson['keywords']>;
  return {
    ...raw,
    name: typeof raw.name === 'string' ? raw.name : '',
    folders: list(raw.folders),
    apiKeys: list(raw.apiKeys),
    allowedModels: list(raw.allowedModels),
    roles: raw.roles && typeof raw.roles === 'object' && !Array.isArray(raw.roles) ? raw.roles : {},
    limits: {turns: lim.turns ?? d.limits.turns, costUsd: lim.costUsd ?? d.limits.costUsd, minutes: lim.minutes ?? d.limits.minutes},
    keywords: {ultrawork: list(kw.ultrawork), plan: list(kw.plan)},
  };
}

export function normalizeView(view: ProfilesView): ProfilesView {
  const file = (view.file ?? {version: 1, profiles: []}) as ProfileFileJson;
  return {
    ...view,
    file: {...file, profiles: (Array.isArray(file.profiles) ? file.profiles : []).map(normalizeProfile)},
    keys: Array.isArray(view.keys) ? view.keys.map(k => ({...k, keys: Array.isArray(k.keys) ? k.keys : []})) : [],
  };
}
