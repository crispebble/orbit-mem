# Spring Boot 4·Jackson 3에서 바뀐 이름

프로젝트가 Spring Boot 4.x와 Jackson 3을 쓰면 이름이 바뀐 것이 많아, Boot 3 기준 기억으로 쓰면 컴파일이 안 된다. 버전은 빌드 파일에서 확인한다.

- 웹 스타터는 `spring-boot-starter-webmvc`다. `spring-boot-starter-web`이 아니다.
- 웹 슬라이스 테스트 스타터는 `spring-boot-starter-webmvc-test`다.
- Flyway는 `spring-boot-starter-flyway` + `org.flywaydb:flyway-database-postgresql` 조합이다.
- Jackson Kotlin 모듈은 `tools.jackson.module:jackson-module-kotlin`이다. `com.fasterxml.jackson.module`이 아니다. import 경로도 함께 바뀐다.
- 컴파일러 인자 `-Xjsr305=strict`, `-Xannotation-default-target=param-property`가 켜져 있으면 애노테이션 타겟이 기본적으로 param과 property에 붙는다. `@field:`, `@get:`을 습관적으로 붙이기 전에 필요한지 확인한다.
- 테스트 구성(JUnit 5 + `kotlin-test-junit5`, 통합 테스트는 Testcontainers 등)은 프로젝트가 쓰는 것을 따른다.
