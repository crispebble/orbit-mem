# Next.js(`scripts.dev`가 `next dev`)

- Next.js와 React 버전, 그리고 App Router(`app/`)인지 Pages Router(`pages/`)인지는 `package.json`과 디렉토리로 확인한다.
- App Router라면 서버 컴포넌트가 기본이다. 훅·이벤트 핸들러·브라우저 API가 필요한 컴포넌트에만 `"use client"`를 붙인다. 습관적으로 최상단에 붙이면 서버 컴포넌트의 이점이 전부 사라진다.
- App Router에서 데이터 페칭은 서버 컴포넌트에서 직접 한다. 클라이언트에서 `useEffect` + `fetch`로 초기 데이터를 가져오지 않는다.
- 스크립트는 `package.json`의 `scripts`에서 확인한다.
