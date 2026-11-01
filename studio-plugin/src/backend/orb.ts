// 뒤쪽 프로세스가 `orb`와 데몬 정보 파일을 다루는 길. Node 표준 라이브러리만 쓴다.
import {execFile} from 'node:child_process';
import {accessSync, constants, readFileSync, statSync} from 'node:fs';
import {homedir} from 'node:os';
import {delimiter, join, win32} from 'node:path';

export type Env = Record<string, string | undefined>;

/** orbit-pi packages/orbit-profiles/src/paths.ts orbitHome와 같은 규칙 */
export function orbitHome(env: Env = process.env, platform: NodeJS.Platform = process.platform): string {
  if (env.ORBIT_HOME) return env.ORBIT_HOME;
  const home = env.HOME || homedir();
  if (platform === 'win32') return win32.join(env.APPDATA || win32.join(home, 'AppData', 'Roaming'), 'orbit');
  if (platform === 'darwin') return join(home, 'Library', 'Application Support', 'orbit');
  return join(env.XDG_CONFIG_HOME || join(home, '.config'), 'orbit');
}

export interface DaemonInfo {
  pid: number;
  url: string;
  listen: string;
  version: string;
  startedAt: string;
}

/** `<home>/runtime/daemon/daemon.json`. 없거나 읽을 수 없거나 url이 없으면 undefined */
export function readDaemonInfo(home: string): DaemonInfo | undefined {
  try {
    const info = JSON.parse(readFileSync(join(home, 'runtime', 'daemon', 'daemon.json'), 'utf8')) as Partial<DaemonInfo>;
    if (!info || typeof info.url !== 'string' || !info.url) return undefined;
    return {
      pid: typeof info.pid === 'number' ? info.pid : 0,
      url: info.url,
      listen: typeof info.listen === 'string' ? info.listen : '',
      version: typeof info.version === 'string' ? info.version : '',
      startedAt: typeof info.startedAt === 'string' ? info.startedAt : '',
    };
  } catch {
    return undefined;
  }
}

const isExecutable = (path: string) => {
  try {
    return statSync(path).isFile() && (accessSync(path, constants.X_OK), true);
  } catch {
    return false;
  }
};

/** GUI로 뜬 프로세스의 PATH는 짧다: 흔한 설치 위치도 본다 */
export function commonBinDirs(env: Env): string[] {
  const home = env.HOME || homedir();
  return ['/opt/homebrew/bin', '/usr/local/bin', join(home, '.local', 'bin'), join(home, 'go', 'bin'), join(home, 'bin')];
}

/**
 * 실행할 orb 절대경로. ORBIT_ORB가 있으면 그것(경로가 아니면 PATH에서 찾는다), 없으면 PATH와 흔한 위치에서 `orb`(옛 별칭 `oni`는 쓰지 않는다).
 * 찾지 못하면 undefined.
 */
export function resolveOrb(env: Env = process.env, exists: (path: string) => boolean = isExecutable): string | undefined {
  const dirs = [...(env.PATH ?? '').split(delimiter).filter(Boolean), ...commonBinDirs(env)];
  const find = (name: string) => (name.includes('/') || name.includes('\\') ? (exists(name) ? name : undefined) : dirs.map(d => join(d, name)).find(exists));
  if (env.ORBIT_ORB) return find(env.ORBIT_ORB);
  return find('orb');
}

export interface OrbResult {
  code: number;
  stdout: string;
  stderr: string;
}

/** orb를 인자로 한 번 실행한다. 0이 아닌 종료도 던지지 않고 돌려준다. 실행 자체가 안 되면 던진다. */
export function runOrb(orb: string, args: string[], env: Env, timeoutMs: number): Promise<OrbResult> {
  return new Promise((resolve, reject) => {
    execFile(orb, args, {env: env as NodeJS.ProcessEnv, timeout: timeoutMs, maxBuffer: 1 << 20, windowsHide: true}, (error, stdout, stderr) => {
      const out = String(stdout);
      const err = String(stderr);
      if (!error) return resolve({code: 0, stdout: out, stderr: err});
      const e = error as NodeJS.ErrnoException & {killed?: boolean};
      // 0이 아닌 종료(code가 숫자)와 시간 초과(killed)는 결과로 돌려주고, 실행 파일을 못 여는 오류(ENOENT 등)는 던진다
      if (typeof e.code === 'number') return resolve({code: e.code, stdout: out, stderr: err});
      if (e.killed) return resolve({code: 1, stdout: out, stderr: `${err}\ntimed out after ${timeoutMs} ms`});
      reject(error);
    });
  });
}

/** 출력의 끝 줄들(오류 문구용) */
export function tail(text: string, lines = 3): string {
  return text.trim().split(/\r?\n/).filter(Boolean).slice(-lines).join('\n');
}
