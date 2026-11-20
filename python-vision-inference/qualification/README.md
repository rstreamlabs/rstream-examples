# Qualify Python vision inference

Run the functional checks, model benchmark, and optional live probes against
the same model weights and reference video. Choose the files explicitly so
the run can record their hashes.

## Local model and lifecycle checks

From `python-vision-inference`, build both roles and run the local profile.

```bash
make build PYTHON=python3.13
python3.13 qualification/run.py \
  --model /absolute/path/to/yolov8n.pt \
  --media /absolute/path/to/reference.mp4
```

The runner exercises framing, frame and result correlation, malformed input,
worker admission, registry updates, selection, and capture teardown. It
repeats the model cancellation regression and checks that an interrupted
session cannot release the model mutex while its executor is still
running. It benchmarks inference on an exact decoded reference frame.

| Option                                 | Default and purpose                                                                                  |
| -------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `--runs`                               | `20` benchmark samples.                                                                              |
| `--concurrency`                        | `8` concurrent benchmark requests.                                                                   |
| `--stress-repetitions`                 | `100` iterations of the cancellation regression.                                                     |
| `--max-p95-ms`, `--min-throughput-fps` | Off by default. Set a model latency and throughput budget for the chosen input size and accelerator. |

## Live worker and transport checks

Configure an isolated rstream context with permission to create and dial
private tunnels and to read inventory. Add `--live` to run through an engine
with no existing test workers to manage. The runner starts two workers with
two session slots each. It verifies overload rejection, releases a slot,
and verifies that it can open another session. It stops worker A during an
active stream, verifies that stream reports EOF, then sends the reference
frame to worker B. The label, confidence, and box signature must match.
A framed echo probe checks byte equality across five payload sizes.

```bash
python3.13 qualification/run.py \
  --model /absolute/path/to/yolov8n.pt \
  --media /absolute/path/to/reference.mp4 \
  --live
```

| Option                                   | Default and purpose                                                                  |
| ---------------------------------------- | ------------------------------------------------------------------------------------ |
| `--live-frames`                          | `5` frames per live session.                                                         |
| `--transport-repetitions`                | `20` repeats per transport payload size.                                             |
| `--max-live-p95-ms`, `--max-failover-ms` | Off by default. Set live round-trip and failover budgets.                            |
| `--max-transport-overhead-p95-ms`        | Off by default. Set the reference-frame budget for additional latency over loopback. |

## Regional routing checks

On a hosted global project, also pass `--regional-routing` with two authorized
regions. The runner fills no slots on either candidate before testing and
presents the remote worker first. It verifies that equal normalized loads are
resolved by the latency measured during concurrent session establishment.

```bash
python3.13 qualification/run.py \
  --model /absolute/path/to/yolov8n.pt \
  --media /absolute/path/to/reference.mp4 \
  --live --regional-routing \
  --local-region eu-west-3 \
  --remote-region us-east-1
```

## Output

Use `--output` for a new local output directory. By default the runner uses
`qualification/results/`. Retain the revision, model and media hashes,
runtime versions, topology, and thresholds with the run. Use configured
budgets for pass or fail decisions about latency and throughput in the test
environment. See the shared [testing guide](../../docs/001-testing.md).
