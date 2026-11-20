# Access PostgreSQL through a private tunnel

Create a private rstream bytestream tunnel from the machine that can reach
PostgreSQL. On the client, the rstream CLI dials that tunnel and provides a
loopback TCP port for standard PostgreSQL clients. PostgreSQL continues to
authenticate database users and enforce their SQL permissions.

## Verify the sample database locally

Use Docker with Compose and Make on the database host. In this directory, run:

```sh
make verify
```

This starts the sample PostgreSQL container and queries its initialized
`notes` table without an rstream connection. Expect one row with the text
`hello from a private PostgreSQL service`. The database stays running on
`127.0.0.1:55432`.

## Prepare the private connection

Install the rstream CLI on both machines and `psql` on the client. Both rstream
processes must use the same project or direct engine and be authorized for
their respective roles.

| Deployment | Configuration |
| --- | --- |
| Hosted rstream Pro or Enterprise | Select the same project on both machines. For an interactive trial, run `rstream login` and `rstream project use <project-endpoint>` on each. Give long-running services scoped credentials. The database side needs `tunnels.tunnels.create-delete`. The client side needs `tunnels.streams.create-delete` for the named tunnel. |
| Direct Community Edition engine | Configure both clients with the same engine address and JWT credentials issued by its operator. For a JWT in a protected file, create a context with `rstream context create postgres-ce --engine <engine-host>:443 --token-file /path/to/agent.jwt --no-api-url`, then export `RSTREAM_CONTEXT=postgres-ce` in each forwarding terminal. |

See [Private Tunnels](https://rstream.io/docs/tunnels/private-tunnels) and
[Fine-grained Tokens](https://rstream.io/docs/security/fine-grained-tokens).

## Create the tunnel and query through it

On the database host, keep the following command running:

```sh
make run
```

This starts PostgreSQL if needed, waits for readiness, then runs
`rstream nc -L rstrm://staging-postgres -R 127.0.0.1:55432`.
The tunnel has no public hostname or port.

On the client, open a local TCP port in a second terminal. This command needs
only the rstream CLI and its selected configuration.

```sh
rstream nc -L 127.0.0.1:15432 -R rstrm://staging-postgres
```

If you have this checkout on the client, `make client-port` runs the same
command. Leave the forwarders running and query from another client terminal.

```sh
psql 'postgres://app:app@127.0.0.1:15432/app' \
  -c 'select id, body from notes order by id limit 5;'
```

Expect the same seeded row. Use this loopback address for a migration tool,
database driver, or CI job running alongside the client forwarder. The
`app:app` credential belongs to the sample database only.

## Connect an existing database

Run `rstream nc -L rstrm://staging-postgres -R <database-host>:5432` from the
machine that can reach your database. The client forwarder can use the same
tunnel name and local port. Use a dedicated PostgreSQL role with the necessary
permissions. For a TLS-enabled database, verify the database certificate
against its own DNS name and trusted CA. With libpq, set `host` to that name,
`hostaddr=127.0.0.1`, `port=15432`, and `sslmode=verify-full`. Set
`sslrootcert` to the CA bundle if your client requires an explicit path.
See the [libpq connection parameters](https://www.postgresql.org/docs/18/libpq-connect.html#LIBPQ-PARAMKEYWORDS).

| Setting | Default and use |
| --- | --- |
| `TUNNEL_NAME` | `staging-postgres`. Set the same name for the server and client. |
| `POSTGRES_HOST_PORT` | `55432`. Loopback port where Compose exposes the sample database. |
| `LOCAL_PORT` | `55432`. Port the server-side CLI connects to. Change this and `POSTGRES_HOST_PORT` together when moving the sample's database port. |
| `CLIENT_PORT` | `15432`. Client-side loopback port. Also update the PostgreSQL connection string if you change it. |

The optional [qualification suite](qualification/) exercises TLS, transaction
semantics, bulk copy, concurrent sessions, cancellation, and recovery in an
isolated non-production rstream context.

## Stop the example

Interrupt both forwarders with Ctrl+C. On the database host, use `make stop`
to stop PostgreSQL and keep its data. Use `make clean` to remove the sample
Compose resources and database volume.
