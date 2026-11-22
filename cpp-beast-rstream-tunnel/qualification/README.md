# Verify the Boost.Beast runtime

Build the [sample](../README.md), then choose an isolated Engine or test
project that permits published HTTP. The runtime test checks the response
body, HTTP keep-alive, concurrent requests and shutdown with an active
request. It needs an HTTPS caller that can reach the created tunnel.

```sh
make verify-runtime RSTREAM_RUNTIME_CONTEXT=my-test-context
```

For repeated measurements, run the harness from the sample directory and
set an output path outside the repository. The destination must be new.

```sh
results_dir=$(mktemp -d)
python3 qualification/run.py --context my-test-context --output "$results_dir/beast"
```

The harness writes a manifest, latency plot and Markdown report. Compare
runs with matching topology and workload. Use `--help` for iteration and
cooldown options.
