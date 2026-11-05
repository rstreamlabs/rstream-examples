import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const candidate = JSON.parse(
  readFileSync(new URL("../sdk-candidate.json", import.meta.url), "utf8"),
);

export function verifyArchive(name, version, bytes, locked) {
  assert.match(name, /^@rstreamlabs\/[a-z-]+$/);
  assert.match(version, /^\d+\.\d+\.\d+$/);
  assert.equal(locked?.version, version);
  assert.equal(
    locked.resolved,
    `https://registry.npmjs.org/${name}/-/${name.split("/")[1]}-${version}.tgz`,
  );
  assert.equal(
    locked.integrity,
    `sha512-${createHash("sha512").update(bytes).digest("base64")}`,
    `SDK archive differs from lockfile: ${name}`,
  );
}

export function verifyCandidate(bytes, expected = candidate) {
  assert.equal(expected.repository, "rstreamlabs/rstream-js");
  assert.match(expected.source, /^[0-9a-f]{40}$/);
  assert.ok(Number.isSafeInteger(expected.runId) && expected.runId > 0);
  assert.ok(
    Number.isSafeInteger(expected.artifactId) && expected.artifactId > 0,
  );
  assert.equal(
    `sha256:${createHash("sha256").update(bytes).digest("hex")}`,
    expected.artifactDigest,
    "Candidate archive differs from the reviewed GitHub artifact",
  );
}

async function download(url, headers = {}) {
  const response = await fetch(url, {
    headers,
    signal: AbortSignal.timeout(30_000),
  });
  if (response.status === 404) return undefined;
  assert.ok(response.ok, `Archive download returned HTTP ${response.status}`);
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    assert.ok(size <= 32 * 1024 * 1024, "Archive exceeds its size limit");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

export async function prepare(project, originalArchive) {
  const lock = JSON.parse(readFileSync(join(project, "package-lock.json")));
  const entries = Object.entries(candidate.packages).filter(
    ([name, version]) =>
      lock.packages[`node_modules/${name}`]?.version === version,
  );
  assert.ok(
    entries.length > 0,
    "No coordinated SDK versions found in lockfile",
  );
  const temporary = mkdtempSync(join(tmpdir(), "rstream-sdk-candidate-"));
  let archive;
  let release;
  try {
    for (const [name, version] of entries) {
      const locked = lock.packages[`node_modules/${name}`];
      const filename = `rstreamlabs-${name.split("/")[1]}-${version}.tgz`;
      assert.equal(
        locked.resolved,
        `https://registry.npmjs.org/${name}/-/${name.split("/")[1]}-${version}.tgz`,
      );
      let bytes = originalArchive ? undefined : await download(locked.resolved);
      if (!bytes) {
        if (!archive) {
          let zip;
          if (originalArchive) zip = readFileSync(originalArchive);
          else {
            assert.ok(
              process.env.GITHUB_TOKEN,
              "Unpublished SDKs require GitHub authentication",
            );
            zip = await download(
              `https://api.github.com/repos/${candidate.repository}/actions/artifacts/${candidate.artifactId}/zip`,
              { Authorization: `Bearer ${process.env.GITHUB_TOKEN}` },
            );
            assert.ok(
              zip,
              "Original SDK artifact unavailable; do not rebuild it",
            );
          }
          verifyCandidate(zip);
          archive = join(temporary, "candidate.zip");
          writeFileSync(archive, zip);
          release = JSON.parse(
            execFileSync("unzip", ["-p", archive, "release.json"]),
          );
          assert.equal(release.source, candidate.source);
        }
        const matching = release.packages.filter(
          (entry) => entry.name === name,
        );
        assert.equal(matching.length, 1);
        assert.equal(matching[0].version, version);
        assert.equal(matching[0].archive, `packages/${filename}`);
        assert.equal(matching[0].integrity, locked.integrity);
        bytes = execFileSync("unzip", ["-p", archive, matching[0].archive], {
          maxBuffer: 32 * 1024 * 1024,
        });
      }
      verifyArchive(name, version, bytes, locked);
      const path = join(temporary, filename);
      writeFileSync(path, bytes);
      execFileSync("npm", ["cache", "add", path], {
        cwd: project,
        stdio: "inherit",
      });
    }
    console.log(
      `Verified ${entries.length} SDK archives from ${candidate.source}.`,
    );
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  assert.ok(
    process.argv[2],
    "Usage: sdk-candidate.mjs <project> [original-artifact.zip]",
  );
  await prepare(
    resolve(process.argv[2]),
    process.argv[3] && resolve(process.argv[3]),
  );
}
