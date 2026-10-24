.DEFAULT_GOAL := build
GO ?= go
ICE_MODFILE := out/ice-patch/go.mod
ICE_GOFLAGS = $(filter-out -modfile=%,$(GOFLAGS)) -modfile=$(abspath $(ICE_MODFILE))

.PHONY: ice-patch test-ice-patch

ice-patch:
	./ice-patch.sh

test-ice-patch:
	$(GO) test -race ../ice-patch/main.go ../ice-patch/main_test.go

IMAGE ?= rstream-video-distributor:local
OUT ?= out
BINARY := $(OUT)/rstream-video-distributor

.PHONY: build test test-qualification-tools test-qualification-compile test-decoded-restarts test-race vet format-check verify integration compose-check image image-test mediamtx mediamtx-test qualify-fanout qualify-native qualify-source clean

build: ice-patch
	mkdir -p $(OUT)
	$(GO) build -modfile=$(ICE_MODFILE) -trimpath -o $(BINARY) ./cmd/rstream-video-distributor

test: ice-patch
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) test ./...

test-qualification-tools:
	node --test qualification/fanout/render.test.mjs

test-qualification-compile: ice-patch
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) test -run '^$$' -tags='integration qualification' ./internal/bridge

test-decoded-restarts: ice-patch
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) test -race -tags=qualification -timeout=90s -count=1 ./internal/whipwhep -run '^TestICERestartDecodesH264ThroughTURN$$'
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) test -race -tags=qualification -timeout=30s -count=1 ./internal/bridge -run '^TestH264ContinuityDecodesAfterLosingASlice$$'

test-race: ice-patch
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) test -race ./...

vet: ice-patch
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) vet ./...

format-check:
	@test -z "$$($(GO) run mvdan.cc/gofumpt@v0.9.2 -l ./cmd ./internal ./mediamtx-build/testdata)"

verify: test-ice-patch format-check vet test test-qualification-tools test-qualification-compile test-decoded-restarts test-race build

integration: ice-patch compose-check image-test
	GOFLAGS="$(ICE_GOFLAGS)" $(GO) test -race -tags=integration ./internal/bridge

compose-check:
	DISTRIBUTOR_ENV_FILE=distributor.env.example docker compose config --quiet

image:
	docker build -f Dockerfile -t $(IMAGE) ..

image-test: image
	./scripts/image-smoke.sh $(IMAGE)

mediamtx:
	python3 mediamtx-build/build.py --verify --output $(OUT)/mediamtx

mediamtx-test: mediamtx
	python3 mediamtx-build/integration.py --binary $(OUT)/mediamtx

qualify-fanout:
	./qualification/fanout/run.sh $(OUT)/qualification/fanout

qualify-native:
	./qualification/native/run.sh $(OUT)/qualification/native

qualify-source:
	RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS=$${RSTREAM_DISTRIBUTOR_SOURCE_CAPACITY_KBPS:-4000} \
		./qualification/source-network/run.sh $(OUT)/qualification/source-network

clean:
	rm -rf $(OUT)
