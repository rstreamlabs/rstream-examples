DOCKER_COMPOSE ?= docker compose
LOCAL_PORT ?= 55432
CLIENT_PORT ?= 15432
TUNNEL_NAME ?= staging-postgres

.PHONY: build run start verify config wait test query server-tunnel client-port stop clean

build: config

run: start wait
	$(MAKE) server-tunnel

start:
	$(DOCKER_COMPOSE) up -d

verify: config test

config:
	$(DOCKER_COMPOSE) config >/dev/null

wait:
	$(DOCKER_COMPOSE) exec -T postgres sh -c 'until pg_isready -h 127.0.0.1 -U app -d app; do sleep 1; done'

test: start wait query

query:
	$(DOCKER_COMPOSE) exec -T postgres psql -h 127.0.0.1 -U app -d app -c 'select id, body from notes order by id limit 5;'

server-tunnel:
	rstream nc -L rstrm://$(TUNNEL_NAME) -R 127.0.0.1:$(LOCAL_PORT)

client-port:
	rstream nc -L 127.0.0.1:$(CLIENT_PORT) -R rstrm://$(TUNNEL_NAME)

stop:
	$(DOCKER_COMPOSE) stop

clean:
	$(DOCKER_COMPOSE) down -v
