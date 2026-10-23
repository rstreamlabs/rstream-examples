# Adopt the ICE concurrency fix

The producer and distributor can update STUN/TURN configuration while Pion
is still gathering connection candidates. Official Pion ICE v4.4.2 reads the
URL list concurrently with those updates. The samples apply a small verified
patch at build time, pending [the upstream fix](https://github.com/pion/ice/pull/1049).
There is no public API change or Engine deployment for this fix.

## Use the samples

Install Go and Git on the build host, then use the Makefile:

```sh
cd producer
make build-provisioning
make test-race
```

For the distributor, use `make build`, `make test-race` and
`make test-decoded-restarts` (the last command also requires FFmpeg).
The Dockerfiles and the producer's static Linux build apply the same patch.
The distributor Docker build context is now `webrtc-video/`, so use `make image`
or `docker build -f distributor/Dockerfile .` from that directory. Its
`Dockerfile.dockerignore` keeps the context limited to required build inputs.

## Adapt an application based on the samples

1. Copy the shared `ice-patch/` directory next to your application directory,
   the sample's `ice-patch.sh`, and the Makefile's `ice-patch` prerequisite
   and `-modfile`/`GOFLAGS` settings. Keep the reviewed patch and hashes together.
   The helper is included in this repository and needs no SDK release.
2. Run preparation from the application's Go module root, then compile and
   test using the generated module file:

   ```sh
   ./ice-patch.sh
   GOFLAGS="-modfile=$PWD/out/ice-patch/go.mod" go test -race ./...
   go build -modfile=out/ice-patch/go.mod -o out/application ./cmd/application
   ```

3. Apply this to every native, cross-compiled, Docker and CI build path. Keep
   `out/` ignored. Cross-build preparation must execute on the build host;
   `ice-patch.sh` already sets the host OS and architecture for that step.
4. Retain `internal/webrtc/ice_urls_test.go` for producer-derived code or
   `internal/whipwhep/ice_urls_test.go` for distributor-derived code. Adjust
   only the Go package name if necessary. Run the complete race suite and a
   real decoded-video test across repeated ICE restarts/credential refreshes.

A change to the rstream SDK version is not required. `go get` alone does not install this fix in a consuming binary: Go does not
inherit replacement directives from dependencies. A direct `go build` that
omits the generated module file still uses unpatched ICE. No change to the
application's session API or additional timeout is required.

The tool accepts exactly official ICE v4.4.2 and validates archive, patch and
source hashes. It creates a private local copy, leaves the shared module
cache and source `go.mod`/`go.sum` intact, and records
`out/ice-patch/provenance.json`. It fails on unsupported versions or changed
generated sources. After a dependency update, remove `out/ice-patch` and
prepare again; review the patch before changing the ICE version.

Verify the build selection:

```sh
go list -modfile=out/ice-patch/go.mod -m github.com/pion/ice/v4
go version -m out/application
```

Both must show the local `./out/ice-patch/ice` replacement. Preserve provenance
with release evidence. Once an official ICE release contains the fix, qualify
that version with the same regressions and remove the preparation step and
alternate-module flags together.
