# PostgreSQL tunnel qualification

This pack exercises PostgreSQL through both sides of a private rstream TCP
tunnel. It keeps the short sample workflow unchanged while measuring the
properties required for database administration and application traffic:

- PostgreSQL TLS remains active through the tunnel;
- commit, rollback, bulk copy, and concurrent sessions preserve their exact
  semantics;
- a cancelled long query terminates promptly and leaves the path usable;
- PostgreSQL, the publishing tunnel, and the local client listener can each be
  interrupted and recovered independently;
- every process and Compose resource is removed after success, failure, signal,
  or timeout.

The runner uses an isolated Compose project, dynamic loopback ports, a unique
private tunnel name, and finite workloads. It never modifies the database from
the quick-start stack.

## Run

Use a non-production context with private-tunnel permissions:

```bash
python3 qualification/run.py --context your-context
```

The result directory contains `manifest.json`, `measurements.json`,
`report.md`, and `recovery.svg`. A run fails when any semantic assertion fails,
average transaction latency exceeds 500 ms, p95 exceeds 750 ms, any transaction
exceeds 2 seconds, throughput falls below 20 transactions per second, bulk copy
falls below 250 kB/s, cancellation takes more than 5 seconds, or a failed
component does not recover within 20 seconds.

Use `--allow-dirty` only while developing the pack. Evidence proposed for the
repository must come from a clean revision.
