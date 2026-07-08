PYTHON ?= python3
VENV := .venv
PY := $(VENV)/bin/python
PIP := $(VENV)/bin/pip
PY_STAMP := $(VENV)/.install-stamp
PYTHONPATH := $(abspath ..)

.PHONY: build run test verify clean

build: $(PY_STAMP)

$(VENV):
	$(PYTHON) -m venv $(VENV)

$(PY_STAMP): requirements.txt | $(VENV)
	$(PIP) install --upgrade pip
	$(PIP) install -r requirements.txt
	$(PIP) check
	touch $(PY_STAMP)

run: build
	PYTHONPATH=$(PYTHONPATH) $(PY) worker.py $(ARGS)

test: build
	PYTHONPATH=$(PYTHONPATH) $(PY) -m py_compile worker.py ../shared/protocol.py
	PYTHONPATH=$(PYTHONPATH) $(PY) worker.py --help >/dev/null
	$(PIP) check

verify: test

clean:
	rm -rf $(VENV) __pycache__ *.pt
