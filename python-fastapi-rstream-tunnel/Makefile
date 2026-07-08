PYTHON ?= python3
VENV := .venv
PY := $(VENV)/bin/python
PIP := $(VENV)/bin/pip
STAMP := $(VENV)/.install-stamp

.PHONY: build run run-private dial test verify clean

build: $(STAMP)

$(VENV):
	$(PYTHON) -m venv $(VENV)

$(STAMP): requirements.txt | $(VENV)
	$(PIP) install --upgrade pip
	$(PIP) install -r requirements.txt
	$(PIP) check
	touch $(STAMP)

run: build
	$(PY) main.py $(ARGS)

run-private: build
	$(PY) main.py --private $(ARGS)

dial: build
	$(PY) dial_client.py $(ARGS)

test: build
	$(PY) -m py_compile main.py dial_client.py
	$(PY) main.py --help >/dev/null
	$(PY) dial_client.py --help >/dev/null
	$(PIP) check

verify: test

clean:
	rm -rf $(VENV) __pycache__
