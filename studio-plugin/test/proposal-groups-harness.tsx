// 제안 묶음 목록을 서버 렌더로 그려 글을 돌려준다(proposal-groups.test.ts가 esbuild로 묶어 부른다).
import {renderToStaticMarkup} from 'react-dom/server';
import {ProposalGroupList, type ProposalGroupListProps} from '../src/parts/index.ts';

export {setLocale} from '../src/i18n/index.ts';

const noop = () => {};

export const render = (p: Pick<ProposalGroupListProps, 'groups'> & Partial<ProposalGroupListProps>) =>
  renderToStaticMarkup(<ProposalGroupList onOpen={noop} onDecideGroup={noop} {...p} />);
