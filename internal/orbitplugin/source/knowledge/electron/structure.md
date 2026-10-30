# Electron 프로젝트 구조

- `package.json`의 `main`이 빌드 산출물을 가리키면 그 산출물을 직접 고치지 않는다. 원본 소스를 고친다. Nextron처럼 Next.js와 Electron을 묶은 프로젝트가 이런 구조다.
- 렌더러가 정적으로 빌드되어 `electron-serve` 같은 것으로 서빙되면 서버 컴포넌트, SSR, `getServerSideProps`, API Routes, 미들웨어를 쓸 수 없다. 서버가 필요한 일은 메인 프로세스로 보낸다.
- 스타일 라이브러리의 메이저 버전은 프로젝트가 쓰는 것에 맞춘다. 예를 들어 Tailwind v3 프로젝트에 v4 문법을 쓰지 않는다.
- 영속 설정(`electron-store` 등)은 메인 프로세스에서 쓰고 렌더러에서 직접 접근하지 않는다.
- 개발·빌드·배포 스크립트는 `package.json`의 `scripts`에서 확인한다. 네이티브 모듈 리빌드를 `postinstall` 같은 기존 설정이 맡는지 본다.
