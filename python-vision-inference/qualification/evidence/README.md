# Vision qualification evidence

Each directory is an immutable qualification run named after the short commit
it validates. A run is accepted here only when its manifest reports a clean
worktree, every functional command passes, and every configured regression
budget passes.

Start with the run's `report.md`. The sibling JSON files contain the exact
machine-readable observations, the failover and routing figures are generated
from those values, and the logs retain the underlying command output. The model
and media are identified by cryptographic hashes.

- [`34aa947`](34aa947/report.md) — distributed Vision model, transport,
  saturation, failover, and regional-selection qualification on staging.
- [`7e512fe`](7e512fe/report.md) — stronger inference-result equivalence across
  failover, plus the same model, transport, saturation, and regional gates.
