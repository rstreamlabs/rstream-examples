# Verify the Boost.Beast runtime

Build the [sample](../README.md) and run the commands below from
`cpp-beast-rstream-tunnel`. Choose an isolated Engine or test project that
permits anonymous requests to published HTTP tunnels. The runtime test checks the response
body, HTTP keep-alive, concurrent requests and shutdown with an active
request. The test host must be able to reach the printed address and trust its HTTPS certificate.

```sh
make verify-runtime RSTREAM_RUNTIME_CONTEXT=my-test-context
```

For repeated measurements, set an output path outside the repository. The destination must be new.

```sh
results_dir=$(mktemp -d)
python3 qualification/run.py --context my-test-context --output "$results_dir/beast"
```

The harness writes a manifest, latency plot and Markdown report. Compare
runs with matching topology and workload. Use `--help` for iteration and
cooldown options.
