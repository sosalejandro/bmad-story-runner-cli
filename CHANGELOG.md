# Changelog

## [0.4.0](https://github.com/sosalejandro/bmad-story-runner-cli/compare/v0.3.1...v0.4.0) (2026-05-23)


### Features

* **install:** embed agents + orchestrator skill + bmad install command (closes [#61](https://github.com/sosalejandro/bmad-story-runner-cli/issues/61)) ([#62](https://github.com/sosalejandro/bmad-story-runner-cli/issues/62)) ([d89c907](https://github.com/sosalejandro/bmad-story-runner-cli/commit/d89c90701cc6c0982a1cc17d4e4e8697ef170eeb))

## [0.3.1](https://github.com/sosalejandro/bmad-story-runner-cli/compare/v0.3.0...v0.3.1) (2026-05-22)


### Bug Fixes

* **sprint:** tolerate file-level top frontmatter + trailing hrules (closes [#58](https://github.com/sosalejandro/bmad-story-runner-cli/issues/58)) ([#59](https://github.com/sosalejandro/bmad-story-runner-cli/issues/59)) ([17df3d1](https://github.com/sosalejandro/bmad-story-runner-cli/commit/17df3d1b6ac9eeb53038efd9e609c39c5b183157))

## [0.3.0](https://github.com/sosalejandro/bmad-story-runner-cli/compare/v0.2.1...v0.3.0) (2026-05-22)


### Features

* **sprint:** `bmad sprint graph` — DOT/mermaid/json DAG visualisation (closes [#47](https://github.com/sosalejandro/bmad-story-runner-cli/issues/47)) ([#53](https://github.com/sosalejandro/bmad-story-runner-cli/issues/53)) ([c719cac](https://github.com/sosalejandro/bmad-story-runner-cli/commit/c719caca02312a27e62303172042a86a2ac4d713))
* **sprint:** epic-level requires_epics + DAG resolver (closes [#46](https://github.com/sosalejandro/bmad-story-runner-cli/issues/46)) ([#50](https://github.com/sosalejandro/bmad-story-runner-cli/issues/50)) ([59138fd](https://github.com/sosalejandro/bmad-story-runner-cli/commit/59138fd18595e0f0176fcd5825c7597690635256))
* **sprint:** validate-deps — cycle + orphan + missing-dep detection (closes [#48](https://github.com/sosalejandro/bmad-story-runner-cli/issues/48)) ([#51](https://github.com/sosalejandro/bmad-story-runner-cli/issues/51)) ([71a99c5](https://github.com/sosalejandro/bmad-story-runner-cli/commit/71a99c5ed54f6d02ba9b30c4cf170ec8974b194f))
* **state:** hydration-priority sort in `story next` (closes [#49](https://github.com/sosalejandro/bmad-story-runner-cli/issues/49)) ([#52](https://github.com/sosalejandro/bmad-story-runner-cli/issues/52)) ([bd81607](https://github.com/sosalejandro/bmad-story-runner-cli/commit/bd81607c895dac00bf05f7ad5875f98ca4bab155))


### Bug Fixes

* **#42:** treat all-zero TOKEN_BREAKDOWN as "unknown", not 0% cache ([#43](https://github.com/sosalejandro/bmad-story-runner-cli/issues/43)) ([8129b89](https://github.com/sosalejandro/bmad-story-runner-cli/commit/8129b89a0bd6982acb716c8e14e7fbca1e466ad2))

## [0.2.1](https://github.com/sosalejandro/bmad-story-runner-cli/compare/v0.2.0...v0.2.1) (2026-05-19)


### Bug Fixes

* **cli:** real version string from runtime/debug.ReadBuildInfo() ([#39](https://github.com/sosalejandro/bmad-story-runner-cli/issues/39)) ([2618223](https://github.com/sosalejandro/bmad-story-runner-cli/commit/26182237c7123f1857b5a6ab90affc24170fb0df))

## [0.2.0](https://github.com/sosalejandro/bmad-story-runner-cli/compare/v0.1.0...v0.2.0) (2026-05-19)


### Features

* **cli:** --scope on `bmad story next` + `bmad story status` (closes [#35](https://github.com/sosalejandro/bmad-story-runner-cli/issues/35)) ([#37](https://github.com/sosalejandro/bmad-story-runner-cli/issues/37)) ([b4093f6](https://github.com/sosalejandro/bmad-story-runner-cli/commit/b4093f60076c91cb4453df80f2985ea1dfdfd6c8))
* **cli:** AI-agent ergonomics — exit codes + doctor + --help-json (closes [#9](https://github.com/sosalejandro/bmad-story-runner-cli/issues/9)) ([#38](https://github.com/sosalejandro/bmad-story-runner-cli/issues/38)) ([f5f264c](https://github.com/sosalejandro/bmad-story-runner-cli/commit/f5f264cf00ef6fc00d99268ad30ec65c7e135ab0))
* **sprint:** infer-deps — derive depends_on from prose Given/Refs (closes [#19](https://github.com/sosalejandro/bmad-story-runner-cli/issues/19)) ([#34](https://github.com/sosalejandro/bmad-story-runner-cli/issues/34)) ([9221a98](https://github.com/sosalejandro/bmad-story-runner-cli/commit/9221a982c93050d806bfecd6a98473c8836b721b))


### Bug Fixes

* **prompts:** token-breakdown reporting suffix on every L3 stage (closes [#20](https://github.com/sosalejandro/bmad-story-runner-cli/issues/20)) ([#36](https://github.com/sosalejandro/bmad-story-runner-cli/issues/36)) ([09fb740](https://github.com/sosalejandro/bmad-story-runner-cli/commit/09fb74087e66d074f4e31c8af1fad54e22cdf817))
* **test:** convert Story 1.2 atlas-section test to positive assertion ([#32](https://github.com/sosalejandro/bmad-story-runner-cli/issues/32)) ([32a36a9](https://github.com/sosalejandro/bmad-story-runner-cli/commit/32a36a930b75c86c52987a047252e45a34bc9943)), closes [#23](https://github.com/sosalejandro/bmad-story-runner-cli/issues/23)
