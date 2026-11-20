# Contributing to rstream examples

Explain each example's purpose, prerequisites, first result and integration choices. Use the owning SDK’s conventions and link to its reference rather than duplicating its complete option list.

Deferred dependency and model changes belong in the [roadmap](roadmap/), separate from the examples’ supported setup.

Run the example’s verification command before proposing a change. Preserve tests for application behavior and add regressions in the SDK or service when that component owns the fix. The [testing guide](docs/001-testing.md) explains local fixtures and optional runtime/performance tools.

Next.js examples carry their own `AGENTS.md` and installed framework documentation; follow the version actually used by the app. Keep lockfiles coordinated with dependency changes. See [SDK archive checks](.github/sdk-dependencies.md) for published and candidate SDK inputs. Preserve the verified build inputs for native media consumers.

Generated run output and internal investigations stay outside version control. If changing a documented command, validate its prerequisites and API usage as well as its syntax. For vulnerabilities, follow [Security](SECURITY.md).
