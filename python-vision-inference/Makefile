.PHONY: build run run-worker run-device test verify clean

build:
	$(MAKE) -C worker build
	$(MAKE) -C device build

run:
	@echo "Choose a role: make run-worker or make run-device" >&2
	@exit 2

run-worker:
	$(MAKE) -C worker run ARGS="$(ARGS)"

run-device:
	$(MAKE) -C device run ARGS="$(ARGS)"

test:
	$(MAKE) -C worker test
	$(MAKE) -C device test

verify:
	$(MAKE) -C worker verify
	$(MAKE) -C device verify

clean:
	$(MAKE) -C worker clean
	$(MAKE) -C device clean
	rm -rf .venv node_modules __pycache__ shared/__pycache__ .cache yolov8n.pt
