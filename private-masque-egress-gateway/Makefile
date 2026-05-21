APP := private-masque-egress-gateway
PROBE := private-masque-egress-probe
GO ?= go
BIN_DIR := ./out/bin
GATEWAY_CMD := ./cmd/gateway
PROBE_CMD := ./cmd/probe

.PHONY: build run run-local verify test test-race format vet clean

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/$(APP) $(GATEWAY_CMD)
	$(GO) build -o $(BIN_DIR)/$(PROBE) $(PROBE_CMD)

verify: test test-race build

run:
	$(GO) run $(GATEWAY_CMD)

run-local:
	PRIVATE_EGRESS_TOKEN=dev $(GO) run $(GATEWAY_CMD) --local --allow-loopback-targets

test: format vet
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

format:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf ./out
