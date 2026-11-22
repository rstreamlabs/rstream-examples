DOCKER_COMPOSE ?= docker compose
NODE ?= node

.PHONY: configure check build run start wait verify config test smoke run-docker logs stop clean

configure:
	$(NODE) scripts/configure.mjs

check:
	$(NODE) --test scripts/config.test.mjs

build: config

run: run-docker

start:
	$(DOCKER_COMPOSE) up -d prometheus grafana
	$(MAKE) wait

wait:
	$(NODE) --env-file=.env scripts/wait-local.mjs

verify: config test

config:
	$(DOCKER_COMPOSE) config >/dev/null

test: smoke

smoke:
	$(NODE) scripts/smoke-local.mjs

run-docker:
	$(DOCKER_COMPOSE) --profile rstream up -d prometheus grafana rstream

logs:
	$(DOCKER_COMPOSE) logs -f prometheus grafana rstream

stop:
	$(DOCKER_COMPOSE) --profile rstream stop

clean:
	$(DOCKER_COMPOSE) --profile rstream down -v
