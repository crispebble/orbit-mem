# Gradle 멀티모듈 규약

- `build-logic` 같은 `includeBuild` 컨벤션 플러그인이 있으면, **새 모듈의 플러그인 블록에 `kotlin("jvm")`이나 `org.springframework.boot`를 직접 적지 않는다. 컨벤션 플러그인 하나를 적용한다.** 공통 설정을 바꿔야 하면 개별 모듈이 아니라 컨벤션 플러그인 쪽을 고친다.
- 모듈이 `apps` → `domains` → `supports` 같은 계층으로 나뉘어 있으면 의존은 그 방향으로만 흐른다. `domains` 사이의 직접 의존은 만들지 않는다. 두 도메인이 같은 것을 필요로 하면 공유 모듈(`shared-kernel`, `domain-core` 같은 이름)로 올린다. `supports`는 어떤 `domains`도 알지 못한다.
