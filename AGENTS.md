# Repository guidance

## Shared conventions

Follow the existing architecture, naming and coding style. Preserve public contracts, authentication, certificate verification and project policy. Keep changes in the component that owns the behavior.

Update user documentation and examples when behavior changes. Run the relevant checks; place regression tests with the owning component. Keep secrets, execution logs, investigation notes and standalone upstream reproductions outside the repository.

## Repository-specific guidance

Each example is a complete application with its own prerequisites and verification command. Follow `CONTRIBUTING.md` and the owning SDK conventions. Preserve immutable SDK/media inputs and application regressions. Keep fixes to SDK or server behavior in their owning repository. Next.js application directories have additional framework instructions.
