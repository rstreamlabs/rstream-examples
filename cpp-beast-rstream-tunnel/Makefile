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

.PHONY: build check-conan-remote clean verify

build: $(BINARY)

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
	$(MAKE) clean
	$(MAKE) build

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

clean:
	rm -rf build CMakeUserPresets.json CMakePresets.json $(OUT)
