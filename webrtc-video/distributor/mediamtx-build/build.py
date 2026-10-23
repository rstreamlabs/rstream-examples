#!/usr/bin/env python3
"""Build the pinned MediaMTX sample runtime with a bounded H264 compatibility patch."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parent
MAX_NALUS = 1024


def run(args, directory, *, capture=False, env=None):
    return subprocess.run(
        args,
        cwd=directory,
        env={**os.environ, "GOWORK": "off", **(env or {})},
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        timeout=900,
    ).stdout


def extract_module(source, directory):
    identity = source["module"] + "@" + source["version"]
    metadata = json.loads(run(["go", "mod", "download", "-json", identity], directory, capture=True))
    if metadata.get("Sum") != source["sum"]:
        raise ValueError(f"unexpected module checksum for {identity}")
    with zipfile.ZipFile(metadata["Zip"]) as archive:
        digest = hashlib.sha256()
        names = sorted(archive.namelist())
        for name in names:
            checksum = hashlib.sha256(archive.read(name)).hexdigest()
            digest.update(f"{checksum}  {name}\n".encode())
        checksum = "h1:" + base64.b64encode(digest.digest()).decode()
        if checksum != source["sum"]:
            raise ValueError(f"module archive content mismatch for {identity}")
        for name in names:
            if not name.startswith(identity + "/"):
                raise ValueError("module archive has an unexpected prefix")
            relative = Path(name[len(identity) + 1 :])
            if relative.is_absolute() or ".." in relative.parts:
                raise ValueError("module archive has an invalid path")
            target = directory / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(archive.read(name))


def prepare(directory, sources):
    media = directory / "mediamtx"
    common = directory / "mediacommon"
    rtsp = directory / "gortsplib"
    for name, source in sources.items():
        destination = directory / name
        destination.mkdir()
        extract_module(source, destination)
    path = common / "pkg/codecs/h264/h264.go"
    original = path.read_text()
    before = "MaxNALUsPerAccessUnit = 50"
    if original.count(before) != 1:
        raise ValueError("the pinned H264 source no longer matches the reviewed patch")
    path.write_text(original.replace(before, f"MaxNALUsPerAccessUnit = {MAX_NALUS}"))
    for test in [common / "pkg/codecs/h264/annexb_test.go", common / "pkg/codecs/h264/avcc_test.go", rtsp / "pkg/format/rtph264/decoder_test.go"]:
        content = test.read_text()
        before = "NALU count (51) exceeds maximum allowed (50)"
        if content.count(before) != 1:
            raise ValueError("the pinned upstream boundary test changed")
        content = content.replace(before, f"NALU count ({MAX_NALUS + 1}) exceeds maximum allowed ({MAX_NALUS})")
        content = content.replace("5*51", "5*(h264.MaxNALUsPerAccessUnit+1)")
        content = content.replace("for range 51 {", "for range h264.MaxNALUsPerAccessUnit + 1 {")
        test.write_text(content)
    run(["go", "mod", "edit", "-replace=github.com/bluenviron/mediacommon/v2=../mediacommon"], media)
    run(["go", "mod", "edit", "-replace=github.com/bluenviron/gortsplib/v5=../gortsplib"], media)
    run(["go", "mod", "download"], media)
    run(["go", "mod", "verify"], media)
    return media


def build(output, verify):
    sources = json.loads((ROOT / "sources.json").read_text())
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="rstream-mediamtx-") as temporary:
        media = prepare(Path(temporary), sources)
        if verify:
            shutil.copyfile(ROOT / "testdata/h264_budget_test.go", media / "h264_budget_test.go")
            run(["go", "test", "-race", "-count=1", "github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"], media)
            run(["go", "test", "-race", "-count=1", "github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"], media)
            run(["go", "test", "-race", "-count=1", "h264_budget_test.go"], media)
        (media / "internal/core/VERSION").write_text("v1.21.2-rstream.1")
        for package in ("internal/servers/hls", "internal/staticsources/rpicamera"):
            run(["go", "generate", "./" + package], media)
        run(["go", "build", "-mod=readonly", "-trimpath", "-ldflags=-s -w", "-o", str(output), "."], media, env={"CGO_ENABLED": "0"})
        notices = [source["module"] + "@" + source["version"] + "\n\n" + (Path(temporary) / name / "LICENSE").read_text() for name, source in sources.items()]
        output.with_suffix(output.suffix + ".LICENSES").write_text("\n\n".join(notices))
    proof = {"sources": sources, "max_nalus": MAX_NALUS, "max_access_unit_bytes": 8 * 1024 * 1024, "sha256": hashlib.sha256(output.read_bytes()).hexdigest()}
    output.with_suffix(output.suffix + ".build.json").write_text(json.dumps(proof, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    build(args.output.resolve(), args.verify)


if __name__ == "__main__":
    main()
