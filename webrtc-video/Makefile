.PHONY: build run test verify clean build-producer run-producer test-producer verify-producer clean-producer build-distributor test-distributor verify-distributor clean-distributor

build: build-producer build-distributor

run: run-producer

test: test-producer test-distributor

verify: verify-producer verify-distributor

clean: clean-producer clean-distributor

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

build-distributor:
	$(MAKE) -C distributor build

test-distributor:
	$(MAKE) -C distributor test

verify-distributor:
	$(MAKE) -C distributor verify

clean-distributor:
	$(MAKE) -C distributor clean
