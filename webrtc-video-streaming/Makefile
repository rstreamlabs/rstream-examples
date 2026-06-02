APP := webrtc-video-streaming
CMD := ./cmd/$(APP)
DOCKERFILE_LINUX := ./Dockerfile.linux
EMBEDDED_WEB ?= 1
TMP_DIR := ./.tmp
NPM_STAMP := ./node_modules/.install-stamp
WEB_STAMP := $(TMP_DIR)/web.stamp
GST_PKG_CONFIG_PACKAGES := \
	gio-2.0 \
	glib-2.0 \
	gobject-2.0 \
	gstreamer-1.0 \
	gstreamer-controller-1.0 \
	gstreamer-base-1.0 \
	gstreamer-app-1.0
WEB_SOURCES := \
	./internal/web/embed/app.ts \
	./internal/web/embed/index.html \
	./internal/web/embed/input.css \
	./package.json \
	./package-lock.json \
	./tailwind.config.js \
	./tsconfig.json

GO_BUILD_FLAGS :=
ifeq ($(EMBEDDED_WEB),0)
GO_BUILD_FLAGS += -tags noembeddedweb
BUILD_PREREQS := check-build-tools
else
BUILD_PREREQS := check-build-tools css
endif

.PHONY: check-go check-c-compiler check-pkg-config check-gstreamer-dev check-npm check-build-tools check-web-tools css build build-provisioning build-no-web run run-local test test-provisioning format check-format clean dist dist-linux-amd64 dist-linux-arm64

check-go:
	@command -v go >/dev/null 2>&1 || { \
		echo "Go is required to build $(APP). Install Go 1.26+ and retry." >&2; \
		exit 1; \
	}

check-c-compiler:
	@command -v cc >/dev/null 2>&1 || command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1 || { \
		echo "A C compiler is required because the GStreamer Go bindings use cgo." >&2; \
		echo "Install Xcode Command Line Tools on macOS, or build-essential on Debian/Ubuntu." >&2; \
		exit 1; \
	}

check-pkg-config:
	@command -v pkg-config >/dev/null 2>&1 || { \
		echo "pkg-config is required to locate GLib and GStreamer development files." >&2; \
		echo "Install pkg-config, then install the GStreamer development packages listed in the README." >&2; \
		exit 1; \
	}

check-gstreamer-dev: check-pkg-config
	@if ! pkg-config --exists $(GST_PKG_CONFIG_PACKAGES); then \
		echo "Missing GLib/GStreamer development dependencies." >&2; \
		echo "Required pkg-config packages: $(GST_PKG_CONFIG_PACKAGES)" >&2; \
		echo "" >&2; \
		pkg-config --print-errors --exists $(GST_PKG_CONFIG_PACKAGES) 2>&1 | sed 's/^/  /' >&2; \
		echo "" >&2; \
		echo "Install the GStreamer development packages listed in the README, or set PKG_CONFIG_PATH if they are installed in a custom prefix." >&2; \
		exit 1; \
	fi

check-npm:
	@command -v npm >/dev/null 2>&1 || { \
		echo "npm is required only when embedding the local viewer UI." >&2; \
		echo "Use 'make build-provisioning' or 'make build EMBEDDED_WEB=0' for the Next.js platform provisioning build." >&2; \
		exit 1; \
	}

check-build-tools: check-go check-c-compiler check-gstreamer-dev

check-web-tools: check-npm

css: $(WEB_STAMP)

$(NPM_STAMP): ./package.json ./package-lock.json | check-web-tools
	npm ci
	touch $(NPM_STAMP)

$(WEB_STAMP): $(NPM_STAMP) $(WEB_SOURCES)
	mkdir -p $(TMP_DIR)
	npm run build
	touch $(WEB_STAMP)

build: $(BUILD_PREREQS)
	go build $(GO_BUILD_FLAGS) -o ./$(APP) $(CMD)

build-provisioning:
	$(MAKE) build EMBEDDED_WEB=0

build-no-web: build-provisioning

run: check-build-tools css
	go run $(CMD) -config ./config.h264.yaml

run-local: check-build-tools css
	mkdir -p ./.tmp
	sed 's/enabled: true/enabled: false/' ./config.h264.yaml > ./.tmp/config.local.yaml
	go run $(CMD) -config ./.tmp/config.local.yaml

test: check-build-tools css
	npm run check:ts
	go test ./...

test-provisioning: check-build-tools
	go test -tags noembeddedweb ./...

format:
	npm run format

check-format:
	npm run format:check

clean:
	rm -rf ./dist ./.tmp ./internal/web/generated ./$(APP)

dist: dist-linux-arm64 dist-linux-amd64

dist-linux-amd64:
	rm -rf ./dist/linux-amd64
	mkdir -p ./dist/linux-amd64
	docker buildx build \
		--platform linux/amd64 \
		-f $(DOCKERFILE_LINUX) \
		--target export \
		--output type=local,dest=./dist/linux-amd64 \
		.

dist-linux-arm64:
	rm -rf ./dist/linux-arm64
	mkdir -p ./dist/linux-arm64
	docker buildx build \
		--platform linux/arm64 \
		-f $(DOCKERFILE_LINUX) \
		--target export \
		--output type=local,dest=./dist/linux-arm64 \
		.
