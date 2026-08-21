# Qualification standard

The examples have two equally important but deliberately separate contracts.

The **usage contract** is the README path a user follows. It must remain short,
idiomatic, and usable without the qualification toolchain. A user should see
the rstream-specific commands, the application command, the expected result,
and the smallest useful troubleshooting section.

The **evidence contract** lives in an optional `qualification/` directory. It
turns the important claims made by the example into repeatable measurements.
It may use containers, fault injection, traffic generators, profilers, or
browser automation, but none of that machinery may leak into the normal usage
path.

## Required pack structure

A qualification pack provides:

- a `README.md` describing the claim, topology, dependencies, risks, and exact
  acceptance thresholds;
- one top-level runner that owns every child process and exits non-zero when a
  threshold is missed;
- deterministic cleanup on success, failure, interruption, and timeout;
- finite workloads with bounded concurrency, queues, disk use, and runtime;
- raw machine-readable measurements plus a concise Markdown report;
- a manifest containing the repository revision, dirty-worktree state, runtime
  versions, test parameters, and the topology required to interpret the run;
- regression tests for analyzers and for every defect found by qualification.

The pack must distinguish a product failure from a harness failure. Missing
dependencies, expired credentials, an unreachable test environment, and an
invalid workload are reported as qualification errors rather than as measured
product regressions.

## Evidence policy

Reports must state what was measured, not what merely appeared to work. Useful
signals include throughput, latency distributions, time to first byte or
token, frame delivery, retransmission, loss, queue depth, resource use,
recovery time, routing fairness, and leaked processes or descriptors. The
appropriate signals depend on the use case.

A committed report qualifies only the revision and environment in its
manifest. Portable CSV, JSON, Markdown, and SVG evidence may be versioned.
Large logs, captures, generated media, credentials, and machine-specific files
remain CI or local artifacts.

Portable evidence contains only the inputs, source revision, platform class,
and tool versions required to interpret and reproduce the measurement.

Every graph must retain its source data and generator. Axes, units, phases,
thresholds, and anomalies must be explicit. A graph is supporting evidence,
not a substitute for an automated verdict.

## Visual evidence

Published charts share one visual language across the repository. Use a light
background, the repository's neutral text and grid colors, green for a passing
measured response, blue for a configured input or reference, amber for a
threshold or warning, and red only for a failed gate or product limitation.
Color must never be the only way to distinguish a signal.

Each chart answers one engineering question. If two signals do not need the
same time axis to establish cause and response, split them. Keep legends,
method notes, and verdicts outside the plotting area; label every axis and use
human-readable units. A reader at mobile width must be able to identify the
injected condition, the measured response, and the acceptance boundary without
zooming or reading the raw report.

Charts are generated from the machine-readable evidence of a clean run. The
generator and source data are versioned with the report, and a renderer test
protects dimensions, escaping, labels, and required series. Do not hand-edit a
generated chart, normalize a failed result into a pass, or publish a chart from
a dirty working tree as release evidence.

The user guide carries only the charts needed to support its principal claims.
The qualification report retains the full counter set, manifests, detailed
tables, and reproduction commands.

## Failure and performance scenarios

Each pack selects scenarios that challenge its actual architecture. The
following matrix is the minimum design target for this repository; a scenario
may be marked not applicable only with a documented reason.

| Use case | Correctness and routing | Stress and performance | Failure and recovery |
| --- | --- | --- | --- |
| Native and Python HTTP | HTTP semantics, streaming bodies, half-close, client identity | concurrency, body sizes, backpressure, latency | engine interruption, cancellation, slow clients, upstream exit |
| Next.js preview | standard and SDK modes, webhook verification | concurrent pages and assets, startup time | rebuild, tunnel reconnect, invalid webhook, process shutdown |
| Homelab | label discovery, public/private boundaries | scrape cost and dashboard responsiveness | container churn, stale labels, engine and collector outage |
| LLM mesh | eligible-worker filtering, load-aware selection, scoped authorization | time to first token, token throughput, fairness, queue saturation | worker churn, cancellation, timeout, partial and total capacity loss |
| Vision inference | compatible-worker selection, frame/result correlation | frame rate, latency distribution, bounded drops, fairness | worker loss, slow worker, malformed frame, reconnect and failover |
| MASQUE egress | policy, TCP, UDP, QUIC, IPv4/IPv6 | concurrent flows, datagram sizes, sustained throughput | denied target, loss, reorder, target loss, gateway restart |
| PostgreSQL | protocol transparency, TLS, migrations and transactions | concurrent sessions, bulk copy, long transactions | half-close, server restart, cancellation, tunnel interruption |
| Published SSH | authentication, host keys, terminal and file transfer | interactive latency, concurrent sessions, sustained transfer | rejected keys, interrupted transfer, server and tunnel restart |
| Netcat media | exact finite media, Go/C++ interoperability, RTP/RTCP | throughput, frame delivery, process and descriptor bounds | injected loss, repair, receiver exit, cancellation, tunnel loss |
| WebRTC video | relay-only ICE, codec and feedback negotiation | adaptation curve, freeze ratio, retransmission, CPU and queues | bandwidth steps, delay, jitter, loss, TURN and producer interruption |

## Review gate

An example is reference-grade only when its quick start is still simple, its
important claims are backed by an automated pack, difficult paths have
regression coverage in the owning SDK or service, and the latest pinned report
meets every declared threshold. A successful process exit without semantic or
performance assertions is never sufficient evidence.
