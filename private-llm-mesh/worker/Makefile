# private-llm-mesh worker — a single Go binary embedding llama.cpp (cgo).
#
# llama.cpp is vendored under third_party/ (cloned at a pinned tag, not committed)
# and built as static libraries; the cgo include/link flags below are derived
# from it, so the Go code carries no machine-specific paths.

APP := worker
GO ?= go
CMAKE ?= cmake
UNAME_S ?= $(shell uname)
UNAME_M ?= $(shell uname -m)
CMD := ./cmd/$(APP)
BIN := bin/$(APP)

LLAMA_CPP_REPO ?= https://github.com/ggml-org/llama.cpp.git
LLAMA_CPP_REF  ?= b9829
LLAMA_CPP_DIR  ?= third_party/llama.cpp
LLAMA_BUILD    := $(LLAMA_CPP_DIR)/build-static
LLAMA_BUILD_JOBS ?=
LLAMA_BUILD_PARALLEL := $(if $(strip $(LLAMA_BUILD_JOBS)),-j$(LLAMA_BUILD_JOBS),-j)
STATICCHECK_VERSION ?= v0.7.0
GOVULNCHECK_VERSION ?= v1.1.4

# Acceleration follows the host: Metal on Apple Silicon, where it is always
# present and several times faster than CPU, and a portable CPU build elsewhere.
# Override on the host, e.g. LLAMA_CMAKE_FLAGS="-DGGML_CUDA=ON" on an NVIDIA box.
ifeq ($(UNAME_S)-$(UNAME_M),Darwin-arm64)
# EMBED_LIBRARY compiles the Metal shaders into the library: without it the
# worker looks for ggml-metal.metal next to the binary at run time and fails.
LLAMA_CMAKE_FLAGS ?= -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON
else
LLAMA_CMAKE_FLAGS ?= -DGGML_METAL=OFF -DGGML_ACCELERATE=OFF
endif

# The stamp encodes both the pinned ref and the build flags, so bumping the pin or
# switching acceleration re-runs the checkout and rebuild instead of silently
# reusing the tree already on disk.
LLAMA_STAMP := $(LLAMA_BUILD)/.built-$(LLAMA_CPP_REF)-$(shell printf '%s' '$(LLAMA_CMAKE_FLAGS)' | cksum | cut -d' ' -f1)

CGO_CXXFLAGS := -std=c++17 -O2 \
	-I$(abspath $(LLAMA_CPP_DIR))/common \
	-I$(abspath $(LLAMA_CPP_DIR))/include \
	-I$(abspath $(LLAMA_CPP_DIR))/ggml/include \
	-I$(abspath $(LLAMA_CPP_DIR))/vendor
# Deferred (=, not :=) so the backend discovery below is evaluated when a recipe
# runs rather than when this file is parsed: on a clean tree the libraries do not
# exist yet at parse time, and an immediate expansion would silently drop the GPU
# backends from the link line.
CGO_LDFLAGS = \
	-L$(abspath $(LLAMA_BUILD))/common \
	-L$(abspath $(LLAMA_BUILD))/src \
	-L$(abspath $(LLAMA_BUILD))/ggml/src \
	-lllama-common -lllama-common-base -lllama -lggml -lggml-cpu -lggml-base
ifeq ($(UNAME_S),Darwin)
CGO_LDFLAGS += -lc++ -framework Accelerate -framework Foundation -framework Metal -framework MetalKit
else
CGO_LDFLAGS += -lstdc++ -lgomp
endif

# Link extra GPU backend libs (Metal, CUDA…) when llama.cpp was built with them,
# discovered from the build tree so no backend is hardcoded here.
CGO_LDFLAGS += $(addprefix -L,$(sort $(dir $(abspath $(wildcard $(LLAMA_BUILD)/ggml/src/*/libggml-*.a))))) $(patsubst lib%.a,-l%,$(notdir $(wildcard $(LLAMA_BUILD)/ggml/src/*/libggml-*.a)))

export CGO_ENABLED := 1
export CGO_CXXFLAGS
export CGO_LDFLAGS

.PHONY: all build run test test-race vet staticcheck vulncheck format check-format check verify clean distclean deps \
	check-go check-cmake check-c-compiler

all: build

## check: reproduce the CI gates locally (format, vet, test, build).
check: check-format vet test test-race build

verify: check staticcheck vulncheck

## deps: clone (pinned) and build llama.cpp static libraries.
deps: $(LLAMA_STAMP)

$(LLAMA_STAMP): | check-cmake check-c-compiler
	@if [ ! -d "$(LLAMA_CPP_DIR)/.git" ]; then \
		echo ">> cloning llama.cpp @ $(LLAMA_CPP_REF)"; \
		git clone $(LLAMA_CPP_REPO) $(LLAMA_CPP_DIR); \
	fi
	@if [ "$$(git -C $(LLAMA_CPP_DIR) rev-parse --verify --quiet HEAD)" != \
	      "$$(git -C $(LLAMA_CPP_DIR) rev-parse --verify --quiet '$(LLAMA_CPP_REF)^{commit}')" ]; then \
		echo ">> checking out llama.cpp @ $(LLAMA_CPP_REF)"; \
		git -C $(LLAMA_CPP_DIR) fetch --tags --quiet origin; \
		git -C $(LLAMA_CPP_DIR) checkout --quiet $(LLAMA_CPP_REF); \
	fi
	@if [ -f "$(LLAMA_BUILD)/CMakeCache.txt" ] && \
	    ! grep -qx "CMAKE_HOME_DIRECTORY:INTERNAL=$(abspath $(LLAMA_CPP_DIR))" "$(LLAMA_BUILD)/CMakeCache.txt"; then \
		echo ">> stale CMake cache (checkout was moved); resetting the build tree"; \
		rm -rf "$(LLAMA_BUILD)"; \
	fi
	@echo ">> building llama.cpp static libraries"
	$(CMAKE) -S $(LLAMA_CPP_DIR) -B $(LLAMA_BUILD) \
		-DBUILD_SHARED_LIBS=OFF -DGGML_CCACHE=OFF $(LLAMA_CMAKE_FLAGS) \
		-DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
		-DLLAMA_BUILD_TOOLS=OFF -DLLAMA_BUILD_SERVER=OFF
	$(CMAKE) --build $(LLAMA_BUILD) --target llama-common $(LLAMA_BUILD_PARALLEL)
	@rm -f $(LLAMA_BUILD)/.built-*
	@touch $@

## build: compile the worker binary (builds deps first).
build: check-go deps
	$(GO) build -o $(BIN) $(CMD)

## run: build then run. Pass flags via ARGS, e.g. make run ARGS="--model m.gguf".
run: build
	$(BIN) $(ARGS)

## test: run Go tests. Set MODEL=/path/to/gguf to include the engine tests.
test: check-go deps
	$(GO) test ./...

test-race: check-go deps
	$(GO) test -race ./...

## vet: run go vet (needs the cgo flags, so it depends on deps and the exported environment).
vet: check-go deps
	$(GO) vet ./...

staticcheck: check-go deps
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

vulncheck: check-go deps
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

format: check-go
	$(GO) fmt ./...

check-format: check-go
	@out="$$(gofmt -l .)"; test -z "$$out" || { echo "gofmt needed:"; echo "$$out"; exit 1; }

clean:
	rm -rf bin

## distclean: also remove the built llama.cpp (keeps the clone).
distclean: clean
	rm -rf $(LLAMA_BUILD)

check-go:
	@command -v go >/dev/null 2>&1 || { \
		echo "Go is required to build $(APP). Install Go 1.26+ from https://go.dev/dl/ and retry." >&2; \
		exit 1; }

check-cmake:
	@command -v cmake >/dev/null 2>&1 || { \
		echo "CMake is required to build the vendored llama.cpp. Install it from https://cmake.org/ and retry." >&2; \
		exit 1; }

check-c-compiler:
	@command -v cc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1 || command -v gcc >/dev/null 2>&1 || { \
		echo "A C/C++ compiler is required: the engine embeds llama.cpp through cgo. Install clang or gcc and retry." >&2; \
		exit 1; }
