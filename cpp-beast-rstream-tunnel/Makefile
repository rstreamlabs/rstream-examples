CMAKE ?= cmake
CONFIG ?= Release
CPPSTD ?= 20
BUILD_ENV ?= env -u CPPFLAGS -u CFLAGS -u CXXFLAGS -u LDFLAGS
CONAN_REMOTE_NAME ?= rstream
CONAN_REMOTE_URL ?= https://nexus.rstream.io/repository/conan
CONAN_OUT ?= .
OUT ?= out
PRESET := conan-$(shell echo $(CONFIG) | tr 'A-Z' 'a-z')
BIN_DIR := $(OUT)/bin
BINARY := $(BIN_DIR)/cpp_beast_rstream_tunnel
SOURCES := $(shell find src -name '*.cpp' -o -name '*.h' -o -name '*.hpp') CMakeLists.txt conanfile.py

.PHONY: build run check-conan-remote clean verify verify-runtime

build: $(BINARY)

run: build
	$(BINARY) $(ARGS)

check-conan-remote:
	@conan remote list | grep -q "^$(CONAN_REMOTE_NAME): $(CONAN_REMOTE_URL)" || { echo "Missing Conan remote '$(CONAN_REMOTE_NAME)'."; echo "Run: conan remote add $(CONAN_REMOTE_NAME) $(CONAN_REMOTE_URL)"; exit 1; }

$(BINARY): $(SOURCES) check-conan-remote | $(BIN_DIR)
	$(BUILD_ENV) conan install . --build=missing -s build_type=$(CONFIG) -s compiler.cppstd=$(CPPSTD) -of $(CONAN_OUT)
	$(BUILD_ENV) $(CMAKE) --preset $(PRESET)
	$(BUILD_ENV) $(CMAKE) --build --preset $(PRESET)
	rm -f $(BINARY)
	$(BUILD_ENV) $(CMAKE) --install build/$(CONFIG) --config $(CONFIG) --prefix $(CURDIR)/$(OUT)
	@echo "Binary available at: $(CURDIR)/$(BINARY)"

verify:
	python3 -m py_compile \
		test/runtime.py test/test_runtime.py \
		qualification/run.py qualification/test_run.py
	PYTHONPATH=test python3 -m unittest discover -s test -p 'test_*.py'
	PYTHONPATH=qualification python3 -m unittest discover -s qualification -p 'test_*.py'
	$(MAKE) clean
	$(MAKE) build

verify-runtime: build
	@test -n "$(RSTREAM_RUNTIME_CONTEXT)" || { echo "RSTREAM_RUNTIME_CONTEXT is required."; exit 1; }
	RSTREAM_RUNTIME_CONTEXT="$(RSTREAM_RUNTIME_CONTEXT)" python3 test/runtime.py

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

clean:
	rm -rf build CMakeUserPresets.json CMakePresets.json $(OUT)
