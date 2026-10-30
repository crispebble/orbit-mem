# 빌드·시험 구성

- **번들러 없이 순수 `tsc`로 빌드하면** 산출물 디렉토리(`dist/` 등)를 직접 고치지 않고, `bin` 항목이 그 안의 파일을 가리키는지 확인한다.
- **테스트가 Node 내장 러너(`node --test`, `node:test`/`node:assert`)면** Jest, Vitest, Mocha를 도입하지 않는다.
- **tsconfig가 여러 개일 수 있다.** 타입 체크용, 배포 빌드용, 테스트 빌드용이 나뉘어 있을 수 있다. 하나만 보고 판단하면 빌드가 깨진다. `npm test`가 타입 체크 → 빌드 → 테스트 빌드 → 테스트 실행 중 무엇을 거치는지 확인한다.
- 실행 파일이 여럿이거나(CLI와 MCP 서버), `skills/`·`plugins/` 같은 확장·배포 디렉토리가 있거나, `pack:check`·`smoke` 같은 검증 스크립트가 있으면 `package.json`의 `scripts`와 `bin`에서 확인하고 해당 검증을 함께 돌린다.
