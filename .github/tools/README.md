# Go analysis tools

The isolated module pins staticcheck and its `golang.org/x/tools` export-data
reader for Go 1.27.2. From `webrtc-video/producer`, for example, run
`../../.github/scripts/staticcheck.sh ./...`. The wrapper
builds from this lock file and analyzes the caller's module.

This avoids the older reader selected by a standalone `go run ...@version`
without changing the analysis rules or the example's runtime dependencies.
