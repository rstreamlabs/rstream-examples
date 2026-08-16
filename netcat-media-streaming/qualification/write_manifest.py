#!/usr/bin/env python3

import argparse
import hashlib
import json
import pathlib
import platform
import shutil
import subprocess
from datetime import datetime, timezone


def tool_identity(command: str | None) -> dict[str, str | None] | None:
    if not command:
        return None
    resolved = shutil.which(command)
    executable = pathlib.Path(resolved or command)
    digest = None
    try:
        with executable.open("rb") as source:
            checksum = hashlib.sha256()
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                checksum.update(chunk)
            digest = checksum.hexdigest()
    except OSError:
        pass
    version = "version unavailable"
    for arguments in ([command, "--version"], [command, "version"]):
        try:
            result = subprocess.run(
                arguments,
                check=False,
                capture_output=True,
                text=True,
                timeout=10,
            )
        except (OSError, subprocess.TimeoutExpired):
            continue
        output = (result.stdout or result.stderr).strip()
        if output:
            version = output.splitlines()[0]
            break
    return {"name": executable.name, "sha256": digest, "version": version}


def build_manifest(
    scenario: str,
    revision: str,
    dirty: bool,
    go_binary: str,
    cpp_binary: str,
    frames: int,
    width: int,
    height: int,
) -> dict[str, object]:
    return {
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "scenario": scenario,
        "git": {"revision": revision, "dirty": dirty},
        "media": {
            "frames": frames,
            "width": width,
            "height": height,
            "pixelFormat": "yuv420p",
        },
        "host": platform.platform(),
        "tools": {
            "goNetcat": tool_identity(go_binary),
            "cppNetcat": tool_identity(cpp_binary),
            "ffmpeg": tool_identity("ffmpeg"),
            "gstreamer": tool_identity("gst-launch-1.0"),
        },
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("output", type=pathlib.Path)
    parser.add_argument("scenario")
    parser.add_argument("revision")
    parser.add_argument("dirty", choices=("true", "false"))
    parser.add_argument("go_binary")
    parser.add_argument("cpp_binary")
    parser.add_argument("frames", type=int)
    parser.add_argument("width", type=int)
    parser.add_argument("height", type=int)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    manifest = build_manifest(
        args.scenario,
        args.revision,
        args.dirty == "true",
        args.go_binary,
        args.cpp_binary,
        args.frames,
        args.width,
        args.height,
    )
    args.output.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
