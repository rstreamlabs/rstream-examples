# Test the examples

Start with the example’s README and its normal verification command. Each integration owns tests for its application behavior; shared protocol and security regressions belong in the SDK or service that implements them.

## Choose the test scope

| Scope                        | Purpose                                                                                                            |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| Unit and local fixture tests | Validate configuration, framing, authorization decisions, cancellation and data handling without a hosted service. |
| Runtime integration          | Exercise the actual example against an explicitly configured engine or dependency.                                 |
| Load and failure scenarios   | Measure bounded workloads, recovery and resource behavior in a declared environment.                               |

Several examples keep reusable runtime and performance tools under `qualification/`. They are optional for the ordinary quick start. Their READMEs document prerequisites, commands and acceptance criteria. Use isolated credentials and explicit targets for tests that create tunnels or call services.

## Results and artifacts

Write generated reports, traces, captures and charts to a local output directory or CI artifacts. Do not commit per-run results, revision inventories or investigation journals. A small synthetic input belongs in test fixtures only when a regression actually consumes it.

Retain enough information with a run to interpret the result: source revision, dependency versions, workload, topology and configured thresholds. Preserve failures and inconclusive observations. Environment-specific timings are not portable product guarantees.

A runner must bound execution and resources, own its child processes and clean up on failure or interruption. Report a missing dependency or unreachable fixture as a harness error, rather than interpreting it as a product measurement. Keep credentials, customer identifiers and private diagnostics out of publishable output.

For source changes, see [Contributing](../CONTRIBUTING.md). Source publication, deployments and cloud campaigns are separate operations.
