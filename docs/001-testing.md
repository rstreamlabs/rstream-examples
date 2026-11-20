# Verify an example

Use the expected result in the application's README for the first run.
When changing the example, start with its local checks and add a runtime
check for the behavior you changed.

| Check | When to run it |
| --- | --- |
| Unit and local fixture tests | Validate application inputs, authorization decisions, framing or lifecycle with local dependencies. |
| Runtime integration | Exercise a documented tunnel, service or framework with a selected Engine and caller. |
| Load and failure scenarios | Use the owning example's `qualification/` tools for a controlled topology, workload and acceptance threshold. |

Use a test project or isolated Engine for commands that start tunnels or
call other services. Keep results, captures and charts in a local output
directory or CI artifacts. Record the inputs and thresholds with any
results you compare. See [Contributing](../CONTRIBUTING.md) for changes to
the examples and their checks.
