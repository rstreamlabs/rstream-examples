# Distributed Vision qualification

This optional pack verifies the reference implementation beyond its normal
quickstart. The guide remains focused on running a device and a worker; the
qualification profile adds bounded stress, real YOLO inference, exact model and
media hashes, raw logs, JSON measurements, and dependency-free SVG charts.

## Local model and lifecycle profile

Build the sample, then supply the exact weights and reference media:

```bash
make build
python3 qualification/run.py \
  --model /absolute/path/yolov8n.pt \
  --media /absolute/path/reference.mp4
```

The runner verifies:

- TypeScript type safety, Python imports, and every worker, device, and framing
  regression test;
- bounded worker admission and explicit overload rejection;
- 100 cancellation races proving that an executor thread cannot outlive the
  model mutex that protects YOLO;
- registry failure, worker cooldown, power-of-two selection, rendezvous
  distribution, duplicate responses, malformed protocol messages, and capture
  teardown;
- detections from the exact real model and media, sequential latency, and
  serialized concurrent throughput;
- absence of credential-shaped values in the evidence.

Optional environment-specific thresholds turn latency observations into gates:

```bash
python3 qualification/run.py \
  --model /absolute/path/yolov8n.pt \
  --media /absolute/path/reference.mp4 \
  --max-p95-ms 80 \
  --min-throughput-fps 12
```

Do not copy those example thresholds blindly. Select them for the exact model,
accelerator, input size, and service objective recorded by the environment.

## Reading the evidence

Each run creates a private directory under `qualification/results/`. Start with
`report.md`: it states the verdict and scope, links every raw log, and embeds
command-duration and model-latency charts. `manifest.json` pins the repository
revision, dirty state, model and media hashes, host, tool versions, parameters,
and thresholds. `model-benchmark.json` contains the machine-readable latency,
throughput, detections, and violations.

Model identities exposed through worker labels and the session protocol are
basenames rather than filesystem paths.

This local profile does not qualify discovery or data transit through a
deployed rstream environment. Add `--live` to exercise two independently
managed workers through the configured rstream context, concurrent sessions,
explicit saturation, recovery, abrupt worker loss, registry removal, byte-exact
transport scaling, and measured failover:

```bash
python3 qualification/run.py \
  --model /absolute/path/yolov8n.pt \
  --media /absolute/path/reference.mp4 \
  --live
```

For a global project, `--regional-routing` also starts equal-capacity workers
in two explicitly named regions. It records both regions in the worker
protocol, presents the remote worker first (the old arbitrary tie-break), and
proves that the production selector now prefers the candidate whose concurrent
session establishment was faster:

```bash
python3 qualification/run.py \
  --model /absolute/path/yolov8n.pt \
  --media /absolute/path/reference.mp4 \
  --live --regional-routing \
  --local-region eu-west-3 \
  --remote-region us-east-1
```

The worker still selects by normalized available capacity first. Latency only
breaks a tie between equally loaded candidates, so a nearby saturated worker
does not beat an idle remote worker. This keeps the reference architecture
decentralized and its quickstart unchanged while avoiding arbitrary remote
selection. The report embeds the live/failover and regional before/after charts.

A dirty report is diagnostic only; release evidence must be rerun from the
clean commit it names. Thresholds are disabled unless supplied because their
correct values depend on the qualified regions, access network, accelerator,
and service objective. In that mode, `PASS` means functional integrity and the
latency/throughput values are observations, not an SLO claim; the generated
report states that distinction prominently.

## Committed reference evidence

[`evidence/34aa947/report.md`](evidence/34aa947/report.md) is the current
reference run. It pins clean commit
`34aa947b7139fe58e6b9a4ea32dfb5100b61318e`, the exact model and media hashes,
all runner parameters, five explicit performance regression budgets, raw logs,
JSON measurements, and generated SVG charts. The run exercised staging in two
regions with 250 cancellation races, 50 sequential and 16 concurrent model
requests, 62 live mesh frames, capacity saturation and recovery, abrupt worker
loss, failover, 30 repetitions across five transport payload sizes, and
regional selection.

Together, the report, measurements, and generated figures make the routing and
performance claims independently reviewable. They qualify that commit on the
recorded hardware and network path; they demonstrate reproducibility and
protect against regressions but do not define a public cross-environment SLO.
