.PHONY: build run test verify clean build-producer run-producer test-producer verify-producer clean-producer

build: build-producer

run: run-producer

test: test-producer

verify: verify-producer

clean: clean-producer

build-producer:
	$(MAKE) -C producer build

run-producer:
	$(MAKE) -C producer run ARGS="$(ARGS)"

test-producer:
	$(MAKE) -C producer test

verify-producer:
	$(MAKE) -C producer verify

clean-producer:
	$(MAKE) -C producer clean
