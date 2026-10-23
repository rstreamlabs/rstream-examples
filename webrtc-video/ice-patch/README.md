# Verified ICE build patch

This build-only helper ships with the video samples. It requires Go and Git,
uses only the Go standard library, and does not depend on a rstream SDK release.
The producer and distributor call it from their own module roots through
`ice-patch.sh`; each receives a separate generated module file and source copy.

The official ICE version, archive hash, original/patched source hashes and
patch hash are pinned in `source.json`. The patch is the URL snapshot fix from
[pion/ice#957](https://github.com/pion/ice/pull/957), resubmitted with stronger
regression coverage in [pion/ice#1049](https://github.com/pion/ice/pull/1049).
Unsupported dependency versions and changed generated source fail preparation.
Never edit the shared Go module cache or generated source to bypass an error.

Run `make test-ice-patch` in `../distributor` to exercise archive integrity,
idempotent preparation, module selection and tamper rejection. Both consumers
also run the public-API URL update/gathering regression in their race suites.
See [the adoption guide](../ICE-PATCH.md) for builds, Docker contexts, verification
and removal when an official upstream release includes the fix.
