PYTHON ?= python3
NPM ?= npm
VENV := .venv
PY := $(VENV)/bin/python
PIP := $(VENV)/bin/pip
PY_STAMP := $(VENV)/.install-stamp
NPM_STAMP := node_modules/.install-stamp
PYTHONPATH := $(abspath ..)

.PHONY: build run test verify clean

build: $(PY_STAMP) $(NPM_STAMP)
	$(NPM) run build

$(VENV):
	$(PYTHON) -m venv $(VENV)

$(PY_STAMP): requirements.txt | $(VENV)
	$(PIP) install --upgrade pip
	$(PIP) install -r requirements.txt
	$(PIP) check
	touch $(PY_STAMP)

$(NPM_STAMP): package.json package-lock.json
	$(NPM) ci
	touch $(NPM_STAMP)

run: build
	PYTHONPATH=$(PYTHONPATH) $(PY) device.py $(ARGS)

test: $(PY_STAMP) $(NPM_STAMP)
	$(NPM) run typecheck
	PYTHONPATH=$(PYTHONPATH) $(PY) -m py_compile device.py ../shared/protocol.py
	PYTHONPATH=$(PYTHONPATH) $(PY) device.py --help >/dev/null
	$(PIP) check

verify: test build

clean:
	rm -rf $(VENV) node_modules web/generated __pycache__ .cache
