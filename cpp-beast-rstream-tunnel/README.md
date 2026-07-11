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
used by the Beast server without also compiling the rstream CLI helpers.

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
