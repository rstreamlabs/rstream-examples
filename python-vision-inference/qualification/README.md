# Distributed Vision qualification

This pack tests the inference path from the model mutex to multi-worker
selection and recovery. It uses a pinned YOLO model and reference video so a
result can be compared across code changes without changing the workload.

## Method

The local profile validates framing, frame/result correlation, malformed input,
worker admission, registry updates, selection, capture teardown, and
cancellation while inference is running in an executor. It then benchmarks the
exact decoded reference frame on the selected accelerator.

The live profile starts two independently registered workers with two session
slots each. It fills the pool, checks explicit overload rejection, releases a
slot, and verifies that capacity becomes usable again. It opens an inference
stream on worker A, stops that worker, waits for the existing stream to report
EOF, and sends the same frame to worker B. The failover verdict requires an
identical canonical signature covering labels, confidence scores, and bounding
boxes from the surviving worker.

The regional profile holds capacity equal and presents the remote worker first.
Both sessions are established concurrently; the selector must choose the lower
measured establishment time. A separate framed echo checks byte equality and
latency across five payload sizes.

## Acceptance gates

- Every functional, race, lifecycle, and malformed-input test passes.
- Cancellation cannot release the model mutex while the executor still owns
  YOLO; the reference run repeats this race 250 times.
- Bounded capacity rejects the excess session and accepts a new session after a
  slot is released.
- Worker loss is observed on the existing stream, and the same frame returns
  from the surviving worker with identical detections inside the configured
  failover budget.
- Transport probes preserve every byte at every payload size.
- Equal-capacity regional selection chooses the lower measured establishment
  latency.
- Model p95, model throughput, live RTT, failover, and transport overhead become
  hard gates when their command-line budgets are set.

## Local model and lifecycle profile

Build the sample, then supply the exact weights and reference media:

```bash
make build
python3 qualification/run.py \
  --model /absolute/path/yolov8n.pt \
  --media /absolute/path/reference.mp4
```

Optional environment-specific thresholds turn latency observations into gates:

```bash
python3 qualification/run.py \
  --model /absolute/path/yolov8n.pt \
  --media /absolute/path/reference.mp4 \
  --max-p95-ms 80 \
  --min-throughput-fps 12
```

Choose these thresholds for the exact model, accelerator, input size, and
service objective recorded by the environment.

## Review the evidence

Each run creates a private directory under `qualification/results/`. Start with
`report.md`: it states the method, acceptance gates, and measured result, then
links each underlying log. `manifest.json` pins the repository
revision, dirty state, model and media hashes, host, tool versions, parameters,
and thresholds. `model-benchmark.json` contains the machine-readable latency,
throughput, detections, and violations.

Model identities exposed through worker labels and the session protocol are
basenames rather than filesystem paths.

Add `--live` to exercise discovery and data transit through two independently
managed workers in the configured rstream project:

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
selection. The report plots the failover timeline and the measured regional
comparison.

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

The report and sibling JSON files qualify that commit on the recorded hardware
and network path. The configured budgets are regression gates for that
environment, not a public cross-environment SLO.
