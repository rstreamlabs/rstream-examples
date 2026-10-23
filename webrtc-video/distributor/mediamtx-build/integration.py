#!/usr/bin/env python3
"""Qualify high-slice H264 playback and recordings through a real MediaMTX process."""

import argparse
import json
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def run(args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=60).stdout


def decoded_hashes(path):
    output = run(["ffmpeg", "-v", "error", "-xerror", "-err_detect", "explode", "-i", str(path), "-map", "0:v:0", "-f", "framemd5", "-"])
    return [line.rsplit(",", 1)[1].strip() for line in output.splitlines() if line and not line.startswith("#")]


def wait_ready(url, process, ready=True):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("MediaMTX exited before the path became ready")
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                if json.load(response).get("ready") is ready:
                    return
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(0.1)
    raise RuntimeError("MediaMTX did not publish the high-slice source")


def qualify(binary, directory):
    qualification_frames = 120
    lookahead_frames = 10
    source_frames = qualification_frames + lookahead_frames
    fixture = directory / "source.h264"
    run(["ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x1024:rate=10", "-frames:v", str(source_frames), "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-x264-params", "slices=64:sliced-threads=0:threads=1:aud=1:repeat-headers=1:keyint=10:min-keyint=10:scenecut=0", "-f", "h264", str(fixture)])
    counts = []
    count = 0
    for nalu in re.split(b"\x00\x00(?:\x00)?\x01", fixture.read_bytes()):
        if not nalu:
            continue
        if nalu[0] & 31 == 9:
            if count:
                counts.append(count)
            count = 0
        count += 1
    counts.append(count)
    if len(counts) != source_frames or min(counts) <= 50:
        raise RuntimeError(f"fixture does not exercise the former limit: {counts}")
    expected = decoded_hashes(fixture)
    if len(expected) != source_frames:
        raise RuntimeError("fixture did not fully decode")
    rtsp, api = port(), port()
    config = directory / "mediamtx.yml"
    config.write_text(f"""logLevel: warn
rtspAddress: 127.0.0.1:{rtsp}
rtspTransports: [tcp]
rtmp: no
hls: no
webrtc: no
srt: no
moq: no
api: yes
apiAddress: 127.0.0.1:{api}
pathDefaults:
  record: yes
  recordPath: {directory}/recordings/%path/%Y%m%d-%H%M%S-%f
  recordPartDuration: 100ms
  recordSegmentDuration: 1s
paths:
  live:
""")
    processes = []
    try:
        with (directory / "server.log").open("w") as server_log, (directory / "publisher.log").open("w") as publish_log:
            server = subprocess.Popen([str(binary), str(config)], stdout=server_log, stderr=subprocess.STDOUT)
            processes.append(server)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError("MediaMTX failed to start")
                try:
                    with socket.create_connection(("127.0.0.1", rtsp), timeout=0.2):
                        break
                except OSError:
                    time.sleep(0.1)
            endpoint = f"rtsp://127.0.0.1:{rtsp}/live"
            publisher = subprocess.Popen(["ffmpeg", "-v", "error", "-re", "-framerate", "10", "-i", str(fixture), "-c", "copy", "-f", "rtsp", "-rtsp_transport", "tcp", endpoint], stdout=publish_log, stderr=subprocess.STDOUT)
            processes.append(publisher)
            wait_ready(f"http://127.0.0.1:{api}/v3/paths/get/live", server)
            captured = directory / "received.h264"
            run(["ffmpeg", "-v", "error", "-rtsp_transport", "tcp", "-i", endpoint, "-t", "5", "-c", "copy", "-f", "h264", str(captured)])
            if publisher.wait(timeout=30) != 0:
                raise RuntimeError("source publication failed")
            wait_ready(f"http://127.0.0.1:{api}/v3/paths/get/live", server, ready=False)
            server.terminate()
            server.wait(timeout=10)
        received = decoded_hashes(captured)
        if len(received) < 40 or not any(expected[i : i + len(received)] == received for i in range(len(expected))):
            raise RuntimeError("received frames differ from the uninterrupted source")
        recordings = sorted((directory / "recordings/live").glob("*.mp4"))
        recorded = [digest for path in recordings for digest in decoded_hashes(path)]
        # Recorder shutdown can stop its asynchronous reader with a final frame
        # still queued; fMP4 also retains a frame until the next DTS arrives.
        # Keep a full GOP beyond the 120 qualification frames so those frames
        # must all be durable. Every additional recorded frame must also match.
        complete = len(recorded) >= qualification_frames
        exact = recorded == expected[:len(recorded)]
        report = {"source_frames": len(expected), "qualification_frames": qualification_frames, "lookahead_frames": lookahead_frames, "received_frames": len(received), "recorded_frames": len(recorded), "recording_segments": len(recordings), "nalus_per_access_unit": {"min": min(counts), "max": max(counts)}, "decoded_frame_hashes_match": complete and exact}
        (directory / "result.json").write_text(json.dumps(report, indent=2) + "\n")
        (directory / "frame-hashes.json").write_text(json.dumps({"source": expected, "received": received, "recorded": recorded}, indent=2) + "\n")
        print(json.dumps(report))
        if not complete or not exact:
            raise RuntimeError(f"recorded qualification frames differ: {len(recorded)}/{qualification_frames}, exact={exact}")
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.output:
        args.output.mkdir(parents=True, exist_ok=False)
        qualify(args.binary.resolve(), args.output.resolve())
    else:
        with tempfile.TemporaryDirectory(prefix="rstream-h264-runtime-") as directory:
            qualify(args.binary.resolve(), Path(directory))


if __name__ == "__main__":
    main()
