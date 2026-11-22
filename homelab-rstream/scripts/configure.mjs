import { randomBytes } from "node:crypto";
import { lstat, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

export async function configure(directory, dockerGid) {
  const destination = path.join(directory, ".env");
  try {
    const existing = await lstat(destination);
    if (!existing.isFile()) throw new Error("Existing .env must be a regular private file, not a symlink.");
    if ((existing.mode & 0o077) !== 0) throw new Error("Existing .env must have mode 0600 before reuse.");
    return false;
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  if (dockerGid === undefined && process.platform !== "linux") throw new Error("Supply --docker-gid from the Docker daemon namespace; host socket metadata is not portable to Docker Desktop.");
  const gid = dockerGid ?? String((await stat("/var/run/docker.sock")).gid);
  if (!/^(0|[1-9][0-9]*)$/.test(gid) || Number(gid) > 4294967295) throw new Error("Docker GID must be an unsigned 32-bit integer.");
  const contents = [
    `GRAFANA_ADMIN_PASSWORD=${randomBytes(32).toString("base64url")}`,
    "GRAFANA_ADMIN_USER=admin",
    "RSTREAM_UID=1000",
    "RSTREAM_GID=1000",
    `DOCKER_GID=${gid}`,
    "RSTREAM_ENGINE=",
    "RSTREAM_AUTHENTICATION_TOKEN=",
    "",
  ].join("\n");
  await writeFile(destination, contents, { flag: "wx", mode: 0o600 });
  return true;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  try {
    const args = process.argv.slice(2);
    if (args.length !== 0 && (args.length !== 2 || args[0] !== "--docker-gid")) throw new Error("Usage: node scripts/configure.mjs [--docker-gid <socket-group-id>]");
    const created = await configure(process.cwd(), args[1]);
    console.log(created ? "Created private .env; inspect it locally for the Grafana password." : "Kept the existing private .env unchanged.");
  } catch (error) {
    console.error(error.code === "ENOENT" ? "Docker socket not found. Supply its container-visible GID with --docker-gid." : error.message);
    process.exitCode = 1;
  }
}
