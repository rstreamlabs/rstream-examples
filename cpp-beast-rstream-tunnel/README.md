# C++ Boost.Beast rstream tunnel

This sample serves a small Boost.Beast HTTP application through a published
rstream HTTP tunnel using the C++ SDK.

It demonstrates the native C++ path where a process keeps its asynchronous HTTP
server code and accepts inbound rstream streams as `rstream::io_rstrm::socket`
instances.

## How it works

The process opens an authenticated control channel with the C++ SDK, creates a
published HTTP tunnel under the selected project, and accepts inbound rstream
streams. Each accepted stream satisfies the Asio stream concepts, so the same
Beast session code that would run on a TCP socket reads and writes HTTP on the
tunnel stream directly. There is no local listener and no reverse proxy; the
only network activity is the outbound runtime session to the engine.

## Build

The sample uses Conan 2 to resolve Boost and the rstream C++ SDK. Its
`conanfile.py` requires `rstream/[>=1.12.0 <2]` and disables SDK utility
binaries for this application build, so a source fallback builds the libraries
used by the Beast server without also compiling the rstream CLI helpers. Its
Boost range matches the SDK's supported Conan range (`>=1.81.0 <1.90.0`);
Boost 1.90 and 1.91 currently expose inconsistent Cobalt package metadata.

```bash
conan profile detect --force
conan remote add rstream https://nexus.rstream.io/repository/conan
make verify
```

The binary is installed under `out/bin/cpp_beast_rstream_tunnel`.

If you already have Boost and `rstream-cpp` installed in a CMake prefix, a
direct CMake build also works.

```bash
cmake -S . -B build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build
```

With a custom prefix:

```bash
cmake -S . -B build -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_PREFIX_PATH=/path/to/rstream-cpp/install
cmake --build build
```

## Run

Select a project or engine with the rstream CLI, then run the server.

```bash
rstream login
rstream project use <project-endpoint> --default
make run
```

The process prints the forwarding address once the tunnel is created.

## Runtime verification

The runtime gate creates two uniquely named, short-lived published tunnels. It
checks idle shutdown, proves that the edge preserves one client HTTP keep-alive
transport across per-request rstream streams, sends 64 concurrent requests,
then leaves a partial HTTP request blocked in `async_read` while it sends
`SIGINT`. The process must cancel that pending session and exit cleanly. The
64-request campaign also enforces a 2 s p95 and a 5 s maximum end-to-end
latency budget; these intentionally generous remote-staging limits catch
serialization and stalled handshakes without pretending to be a local
microbenchmark.
Point it at a non-production project context:

```bash
make verify-runtime RSTREAM_RUNTIME_CONTEXT=<context>
```

## Reproducible qualification evidence

For release evidence, build the sample and run five paced campaigns from a
clean commit. The qualification pack reuses the runtime gate, records the
binary hash, individual and aggregate latency summaries, thresholds, and the
Git revision, then generates a dependency-free SVG and a concise report:

```bash
make build
RSTREAM_RUNTIME_CONTEXT=<staging-context> python3 qualification/run.py
```

Results remain local under `qualification/results/`. A committed reference
run is accepted only from a clean worktree and after its manifest reports
`dirty: false`; it qualifies the recorded commit and staging path, not every
possible client network. `--allow-dirty` exists only for local diagnostics.

The latest reviewed reference run is the
[`7500baa` staging qualification](qualification/evidence/7500baa/report.md):
320 byte-exact responses, p95 765.9 ms, maximum 1260.2 ms, clean revision, and
no runtime or shutdown error.
