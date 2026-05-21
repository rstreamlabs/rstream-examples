DOCKER_COMPOSE ?= docker compose
NODE ?= node

.PHONY: start wait verify config test smoke run-docker logs stop clean

start:
	$(DOCKER_COMPOSE) up -d prometheus grafana
	$(MAKE) wait

wait:
	$(NODE) scripts/wait-local.mjs

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
