# Follow the UI generator dependency fix

The LLM sample's shadcn development tooling still pulls in an unpatched
[deepmerge advisory](https://github.com/advisories/GHSA-gcqx-w48m-w2h4).
The production dependency audit is clean; the full audit continues to report it.

Update the generator when a corrected release is available, then verify component
generation and the sample build. Keep the audit visible until it is resolved.
