# Private PostgreSQL access without a VPN

This example exposes a PostgreSQL database through a private rstream bytestream
tunnel. The database is never published as a public Internet endpoint. Standard
tools still connect to a local TCP port on the client machine.

The shape is useful for development, migrations, one-off support work, CI jobs,
or internal admin tools that need database access without a VPN or inbound
firewall rule.

## Start the database

```bash
make verify
```

The local PostgreSQL container listens on `127.0.0.1:55432` and contains a small
`notes` table.

## Create the private rstream tunnel

On the machine that can reach PostgreSQL:

```bash
make server-tunnel
```

This is equivalent to:

```bash
rstream nc -L rstrm://staging-postgres -R 127.0.0.1:55432
```

The tunnel is private. It has no public hostname, and clients must use rstream
dialing permissions to reach it.

## Open a local client port

On the client machine:

```bash
make client-port
```

This is equivalent to:

```bash
rstream nc -L 127.0.0.1:15432 -R rstrm://staging-postgres
```

Now normal PostgreSQL clients can use `127.0.0.1:15432`:

```bash
psql "postgres://app:app@127.0.0.1:15432/app"
```

The same connection string works for Prisma, Drizzle, `node-postgres`,
migration tools, and CI tasks that accept a standard PostgreSQL URL.

## Security notes

Keep the database tunnel private by default. The database protocol stays
end-to-end between the client and PostgreSQL, while rstream controls who may
dial the private tunnel. On hosted rstream, private tunnel dialing requires a
project plan that supports private tunnels. Self-hosted CE supports private
bytestream tunnels for direct engine deployments.

Use a dedicated database role for tunneled workflows, and keep normal database
authentication enabled. rstream controls access to the network path; PostgreSQL
still owns database identity, authorization, and audit behavior.
