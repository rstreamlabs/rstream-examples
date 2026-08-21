GO ?= go
IMAGE ?= rstream-video-distributor:local
OUT ?= out
BINARY := $(OUT)/rstream-video-distributor

.PHONY: build test test-qualification-compile test-race vet format-check verify integration compose-check image image-test qualify-fanout qualify-native clean

build:
	mkdir -p $(OUT)
	$(GO) build -trimpath -o $(BINARY) ./cmd/rstream-video-distributor

test:
	$(GO) test ./...

test-qualification-compile:
	$(GO) test -run '^$$' -tags='integration qualification' ./internal/bridge

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

format-check:
	@test -z "$$($(GO) run mvdan.cc/gofumpt@v0.9.2 -l ./cmd ./internal)"

verify: format-check vet test test-qualification-compile test-race build

integration: compose-check image-test
	$(GO) test -race -tags=integration ./internal/bridge

compose-check:
	DISTRIBUTOR_ENV_FILE=distributor.env.example docker compose config --quiet

image:
	docker build -t $(IMAGE) .

image-test: image
	./scripts/image-smoke.sh $(IMAGE)

qualify-fanout:
	./qualification/fanout/run.sh $(OUT)/qualification/fanout

qualify-native:
	./qualification/native/run.sh $(OUT)/qualification/native

clean:
	rm -rf $(OUT)
