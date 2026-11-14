# Coordinated SDK dependencies

The web examples install published SDKs with `npm ci` using their checked-in lockfiles. CI starts with a fresh npm cache so a local package cannot silently satisfy a registry dependency.

To verify a retained SDK archive against one example’s lockfile, run from the repository root:

```sh
node .github/scripts/sdk-candidate.mjs webrtc-video/platform /path/to/original-artifact.zip
```

Use `nextjs-rstream-preview` or `private-llm-mesh/web` for the other applications. The helper verifies the archive source and checksum against `.github/sdk-candidate.json` and every SDK package against the selected lockfile. Use the original reviewed archive; rebuilding an archive can change its integrity even when source files match.
