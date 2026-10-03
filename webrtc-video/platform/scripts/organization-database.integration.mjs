// A disposable PostgreSQL instance; never reads or mutates the configured database.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { randomUUID } from "node:crypto"
import { readFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { setTimeout as delay } from "node:timers/promises"

const name = `rstream-video-organization-${randomUUID()}`
const root = resolve(import.meta.dirname, "..")
let started = false
const docker = (...args) =>
  execFileSync("docker", args, {
    encoding: "utf8",
    timeout: 60_000,
    stdio: ["pipe", "pipe", "pipe"],
  }).trim()
try {
  docker(
    "run",
    "--rm",
    "-d",
    "--name",
    name,
    "-e",
    "POSTGRES_PASSWORD=qualification",
    "-p",
    "127.0.0.1::5432",
    "postgres:17-alpine",
  )
  started = true
  let ready = false
  for (let attempt = 0; attempt < 60; attempt++) {
    try {
      docker("exec", name, "pg_isready", "-U", "postgres")
      ready = true
      break
    } catch {
      await delay(250)
    }
  }
  assert.ok(ready, "PostgreSQL readiness deadline")
  const address = docker("port", name, "5432/tcp")
  const databaseURL = `postgresql://postgres:qualification@${address}/postgres`
  // Apply old migrations and seed a personal device before the new migration.
  const sql = (input) =>
    execFileSync(
      "docker",
      [
        "exec",
        "-i",
        name,
        "psql",
        "-U",
        "postgres",
        "-v",
        "ON_ERROR_STOP=1",
        "-At",
      ],
      {
        input,
        encoding: "utf8",
        timeout: 10_000,
        stdio: ["pipe", "pipe", "pipe"],
      },
    ).trim()
  for (const migration of [
    "20260418210000_init",
    "20260506150000_unique_device_name",
    "20260602123000_device_online_since",
  ]) {
    sql(
      readFileSync(
        join(root, "prisma/migrations", migration, "migration.sql"),
        "utf8",
      ),
    )
  }
  sql(`INSERT INTO users (id,"updatedAt") VALUES ('alice',now()),('bob',now());
    INSERT INTO devices (id,"userId",name,"secretHash","secretPrefix","tunnelName","updatedAt") VALUES ('personal','alice','camera','hash1','prefix','tunnel1',now());`)
  sql(
    readFileSync(
      join(
        root,
        "prisma/migrations/20261003100000_device_organization/migration.sql",
      ),
      "utf8",
    ),
  )
  assert.equal(
    sql(
      `SELECT "userId" || ':' || "createdById" FROM devices WHERE id='personal'`,
    ),
    "alice:alice",
  )
  sql(
    `INSERT INTO devices (id,"organizationId","createdById",name,"secretHash","secretPrefix","tunnelName","updatedAt") VALUES ('shared','42','alice','camera','hash2','prefix','tunnel2',now());`,
  )
  assert.throws(() =>
    sql(`UPDATE devices SET "organizationId"='42' WHERE id='personal'`),
  )
  assert.throws(() =>
    sql(`UPDATE devices SET "userId"=null WHERE id='personal'`),
  )
  assert.equal(sql(`SELECT count(*) FROM devices WHERE "userId"='bob'`), "0")
  assert.equal(
    sql(
      `SELECT count(*) FROM devices WHERE "organizationId"='42' AND "userId" IS NULL`,
    ),
    "1",
  )
  sql(`DELETE FROM users WHERE id='alice'`)
  assert.equal(sql(`SELECT count(*) FROM devices WHERE id='personal'`), "0")
  assert.equal(
    sql(
      `SELECT count(*) FROM devices WHERE id='shared' AND "createdById" IS NULL`,
    ),
    "1",
  )
  // Validate the exact deployment migration path independently on a fresh DB.
  sql("CREATE DATABASE fresh")
  execFileSync(join(root, "node_modules/.bin/prisma"), ["migrate", "deploy"], {
    cwd: root,
    env: {
      ...process.env,
      POSTGRES_PRISMA_DIRECT_URL: databaseURL.replace(/\/postgres$/, "/fresh"),
    },
    encoding: "utf8",
    timeout: 60_000,
  })
  console.log(
    "PASS: old inventory migration, owner constraints, creator deletion, fresh deployment",
  )
} finally {
  if (started) docker("rm", "-f", name)
}
