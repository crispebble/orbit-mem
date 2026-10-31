// 대화 창·프로필 화면을 서버 렌더로 그려 글을 돌려준다(chat-render.test.ts가 esbuild로 묶어 부른다). 효과(useEffect)는 돌지 않는다.
import {renderToStaticMarkup} from 'react-dom/server';
import type {PluginContext} from '@onidot-labs/orbit-studio-plugin';
export {setLocale} from '../src/i18n/index.ts';
import {ComposerView} from '../src/chat/composer-view.tsx';
import {emptyForm} from '../src/chat/composer.ts';
import {DecisionCard} from '../src/chat/decision-card.tsx';
import {Transcript} from '../src/chat/transcript.tsx';
import {buildTimeline} from '../src/chat/timeline.ts';
import {ProfileList} from '../src/profiles/view.tsx';
import {ProfileEditor} from '../src/profiles/tab.tsx';
import type {DaemonEvent} from '../src/daemon/api.ts';
import {createDaemonClient} from '../src/daemon/client.ts';
import {sampleProfiles, sampleUsage} from './daemon-fake.ts';

const noop = () => {};
const ctx = {
  scope: {current: () => ({kind: 'project', id: 'p', name: 'P', directories: ['/Users/me/proj']}), onChange: () => noop},
  tabs: {open: noop, setTitle: noop, setParams: noop, setModified: noop, close: noop},
  ui: {ask: async () => null},
} as unknown as PluginContext;

const backend = {call: async () => ({}) as never, on: () => noop};
const client = createDaemonClient(backend as never);
export const messages = (m: Record<string, string>) => {
  (client as unknown as {_m: Record<string, string>})._m = m;
};
const translating = createDaemonClient({call: async (method: string) => (method === 'daemon.messages' ? {lang: 'ko', messages: {'warning.x': '경고 %s', 'option.allow': '허용'}} : {}), on: () => noop} as never);
export const loadKo = () => translating.loadMessages('ko');

export const renderComposer = (over = {}) => renderToStaticMarkup(<ComposerView ctx={ctx} client={client} form={emptyForm({cwd: '/Users/me/proj', ...over})} setForm={noop} onStarted={noop} />);

export const renderCard = (kind: 'tool' | 'question', useTranslating = false) => {
  const c = useTranslating ? translating : client;
  const entry = buildTimeline([
    {seq: 1, runId: 'r', type: 'decision.asked', at: 'x', event: {type: 'decision.asked', at: 'x', id: 'd', title: 'Run rm -rf?', options: ['allow', 'deny'], kind, context: 'Bash: rm -rf build'}} as DaemonEvent,
  ])[0];
  if (entry.kind !== 'decision') throw new Error('x');
  return renderToStaticMarkup(<DecisionCard entry={entry} question={entry.title} optionLabel={c.optionLabel} onAnswer={async () => ({accepted: true})} />);
};

export const renderTranscript = (events: DaemonEvent[], answerable = true, useTranslating = false) => {
  const c = useTranslating ? translating : client;
  return renderToStaticMarkup(<Transcript entries={buildTimeline(events)} actions={{text: c.text, optionLabel: c.optionLabel, answer: async () => ({accepted: true}), answerable}} />);
};

export const renderProfileList = () => {
  const view = sampleProfiles();
  return renderToStaticMarkup(<ProfileList ctx={ctx as never} view={view} usage={sampleUsage().profiles} api={client.api} adding={false} setAdding={noop} newName="" setNewName={noop} />);
};

export const renderEditor = (name = 'work') => {
  const view = sampleProfiles();
  return renderToStaticMarkup(
    <ProfileEditor ctx={ctx} tabId={name} client={client} view={view} profile={view.file.profiles.find(p => p.name === name)!} usage={sampleUsage().profiles[0]} reload={async () => {}} onRenamed={noop} />,
  );
};
