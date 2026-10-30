# ESM(`"type": "module"`)

- **`"type": "module"`이면 ESM이다.** 상대 경로 import에 `.js` 확장자를 붙여야 한다. TypeScript 소스에서 `./foo.ts`가 아니라 `./foo.js`로 쓴다. `__dirname`과 `__filename`이 없다. 필요하면 `import.meta.url` + `fileURLToPath`로 만든다. `require`를 쓰지 않는다.
