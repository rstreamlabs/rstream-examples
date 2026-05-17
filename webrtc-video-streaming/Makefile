APP := webrtc-video-streaming
CMD := ./cmd/$(APP)
DOCKERFILE_LINUX := ./Dockerfile.linux
TMP_DIR := ./.tmp
NPM_STAMP := ./node_modules/.install-stamp
WEB_STAMP := $(TMP_DIR)/web.stamp
WEB_SOURCES := \
	./internal/web/embed/app.ts \
	./internal/web/embed/index.html \
	./internal/web/embed/input.css \
	./package.json \
	./package-lock.json \
	./tailwind.config.js \
	./tsconfig.json

.PHONY: css build run run-local test format check-format clean dist dist-linux-amd64 dist-linux-arm64

css: $(WEB_STAMP)

$(NPM_STAMP): ./package.json ./package-lock.json
	npm ci
	touch $(NPM_STAMP)

$(WEB_STAMP): $(NPM_STAMP) $(WEB_SOURCES)
	mkdir -p $(TMP_DIR)
	npm run build
	touch $(WEB_STAMP)

build: css
	go build -o ./$(APP) $(CMD)

run: css
	go run $(CMD) -config ./config.h264.yaml

run-local: css
	mkdir -p ./.tmp
	sed 's/enabled: true/enabled: false/' ./config.h264.yaml > ./.tmp/config.local.yaml
	go run $(CMD) -config ./.tmp/config.local.yaml

test: css
	npm run check:ts
	go test ./...

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
