# Serve Boost.Beast HTTP on an Asio tunnel

This C++ application accepts rstream sockets with Boost.Asio coroutines and
passes them to a Beast HTTP session. The application opens the outbound
tunnel and owns the HTTP handler.

## Build the application

Use a C++20 compiler, CMake 3.22 or newer, Conan 2, Python 3 and Make.
Install Ninja for Conan source builds. From the root of the examples checkout, enter the C++ sample.

```sh
cd cpp-beast-rstream-tunnel
```

On macOS or Linux, use Conan's default profile and the rstream package remote. Run `conan profile detect` and add the remote only if they
are missing.

```sh
conan profile detect
conan remote add rstream https://nexus.rstream.io/repository/conan
make build
```

The binary is installed at `out/bin/cpp_beast_rstream_tunnel`. The Conan
recipe resolves Boost and the C++ SDK and can build missing packages from
source.

## Open a tunnel and make a request

Install the [rstream CLI](https://rstream.io/docs/getting-started/installation)
to configure the connection. For a hosted project that allows HTTP publication,
sign in if needed.

```sh
rstream login
```

After browser approval, list the projects your account can access.

```sh
rstream project list
```

Replace `my-project-endpoint` with the endpoint from the list.

```sh
rstream project use my-project-endpoint
```

For a self-hosted Engine, prepare a
[direct Engine context](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context)
with its agent credential and CA bundle. Use the same `RSTREAM_CONFIG`
if you stored that context in a separate file.

With your hosted or self-hosted context selected, start the application.

```sh
./out/bin/cpp_beast_rstream_tunnel
```

In another terminal, request the printed `Forwarding address`. Expect HTTP 200 with
`Hello from Boost.Beast through rstream`. Use an authorized caller
credential if required and trust the Engine CA if private. Ctrl+C closes
the client and stops the server.

## Reuse the Beast session in your app

The listener uses `io_rstrm::client` and passes each accepted
`io_rstrm::socket` to a coroutine. Beast reads and writes that socket
through the same asynchronous stream interface it uses with TCP.
Read the [integration guide](https://rstream.io/guides/expose-a-cpp-boost-beast-http-server-with-rstream)
to compare the two paths.

| Setting | Use |
| --- | --- |
| `RSTREAM_TUNNEL_NAME` | Name under which the server is registered. Defaults to `cpp-beast-http`. |
| `RSTREAM_CONFIG`, `RSTREAM_CONTEXT` | Select the runtime's CLI-compatible configuration and context. |

If Boost and the SDK are installed in a CMake prefix, configure directly
using that prefix. This command uses Ninja.

```sh
cmake -S . -B build/direct -G Ninja -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_PREFIX_PATH=/path/to/sdk
cmake --build build/direct
```

Run `make verify` for the clean source build and local test suite. Use
[the runtime harness](qualification) to test concurrent requests and shutdown
on an isolated Engine.
