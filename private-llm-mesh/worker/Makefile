# private-llm-mesh worker — a single Go binary embedding llama.cpp (cgo).
#
# llama.cpp is vendored under third_party/ (cloned at a pinned tag, not committed)
# and built as static libraries; the cgo include/link flags below are derived
# from it, so the Go code carries no machine-specific paths.

APP := worker
GO ?= go
CMAKE ?= cmake
CMD := ./cmd/$(APP)
BIN := bin/$(APP)

LLAMA_CPP_REPO ?= https://github.com/ggml-org/llama.cpp.git
LLAMA_CPP_REF  ?= b9829
LLAMA_CPP_DIR  ?= third_party/llama.cpp
LLAMA_BUILD    := $(LLAMA_CPP_DIR)/build-static
LLAMA_LIB      := $(LLAMA_BUILD)/common/libllama-common.a
LLAMA_BUILD_JOBS ?=
LLAMA_BUILD_PARALLEL := $(if $(strip $(LLAMA_BUILD_JOBS)),-j$(LLAMA_BUILD_JOBS),-j)

# Acceleration is off by default for a portable CPU build. Override on the host,
# e.g. LLAMA_CMAKE_FLAGS="-DGGML_METAL=ON" (macOS) or "-DGGML_CUDA=ON" (Linux).
LLAMA_CMAKE_FLAGS ?= -DGGML_METAL=OFF -DGGML_ACCELERATE=OFF

CGO_CXXFLAGS := -std=c++17 -O2 \
	-I$(abspath $(LLAMA_CPP_DIR))/common \
	-I$(abspath $(LLAMA_CPP_DIR))/include \
	-I$(abspath $(LLAMA_CPP_DIR))/ggml/include \
	-I$(abspath $(LLAMA_CPP_DIR))/vendor
CGO_LDFLAGS := \
	-L$(abspath $(LLAMA_BUILD))/common \
	-L$(abspath $(LLAMA_BUILD))/src \
	-L$(abspath $(LLAMA_BUILD))/ggml/src \
	-L$(abspath $(LLAMA_BUILD))/ggml/src/ggml-blas \
	-lllama-common -lllama-common-base -lllama -lggml -lggml-cpu -lggml-blas -lggml-base -lc++
ifeq ($(shell uname),Darwin)
CGO_LDFLAGS += -framework Accelerate -framework Foundation -framework Metal -framework MetalKit
endif

# Link extra GPU backend libs (Metal, CUDA…) when llama.cpp was built with them.
# Discovered after `make deps`, so the documented "deps then build" flow picks
# them up without hardcoding a backend.
CGO_LDFLAGS += $(addprefix -L,$(sort $(dir $(abspath $(wildcard $(LLAMA_BUILD)/ggml/src/*/libggml-*.a))))) $(patsubst lib%.a,-l%,$(notdir $(wildcard $(LLAMA_BUILD)/ggml/src/*/libggml-*.a)))

export CGO_ENABLED := 1
export CGO_CXXFLAGS
export CGO_LDFLAGS

.PHONY: all build run test vet format check-format check verify clean distclean deps \
	check-go check-cmake check-c-compiler

all: build

## check: reproduce the CI gates locally (format, vet, test, build).
check: check-format vet test build

verify: check

## deps: clone (pinned) and build llama.cpp static libraries.
deps: $(LLAMA_LIB)

$(LLAMA_LIB): | check-cmake check-c-compiler
	@if [ ! -d "$(LLAMA_CPP_DIR)/.git" ]; then \
		echo ">> cloning llama.cpp @ $(LLAMA_CPP_REF)"; \
		git clone $(LLAMA_CPP_REPO) $(LLAMA_CPP_DIR) && \
		git -C $(LLAMA_CPP_DIR) checkout --quiet $(LLAMA_CPP_REF); \
	fi
	@echo ">> building llama.cpp static libraries"
	$(CMAKE) -S $(LLAMA_CPP_DIR) -B $(LLAMA_BUILD) \
		-DBUILD_SHARED_LIBS=OFF -DGGML_CCACHE=OFF $(LLAMA_CMAKE_FLAGS) \
		-DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF \
		-DLLAMA_BUILD_TOOLS=OFF -DLLAMA_BUILD_SERVER=OFF
	$(CMAKE) --build $(LLAMA_BUILD) --target llama-common $(LLAMA_BUILD_PARALLEL)

## build: compile the worker binary (builds deps first).
build: check-go deps
	$(GO) build -o $(BIN) $(CMD)

## run: build then run. Pass flags via ARGS, e.g. make run ARGS="--model m.gguf".
run: build
	$(BIN) $(ARGS)

## test: run Go tests. Set MODEL=/path/to/gguf to include the engine tests.
test: check-go deps
	$(GO) test ./...

## vet: run go vet (needs the cgo flags, so it depends on deps and the exported environment).
vet: check-go deps
	$(GO) vet ./...

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
