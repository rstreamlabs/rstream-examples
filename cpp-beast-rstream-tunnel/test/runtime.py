#!/usr/bin/env python3

import concurrent.futures
import http.client
import math
import os
import secrets
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
from pathlib import Path


EXPECTED_BODY = b"Hello from Boost.Beast through rstream\n"
MAX_LATENCY_BUDGET_MS = 5_000.0
P95_LATENCY_BUDGET_MS = 2_000.0
REQUEST_COUNT = 64
START_TIMEOUT = 30.0
STOP_TIMEOUT = 15.0
WORKER_COUNT = 16


def parse_forwarding_url(value):
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ValueError(f"invalid forwarding address {value!r}")
    if parsed.path not in {"", "/"} or parsed.query or parsed.fragment:
        raise ValueError(f"unexpected forwarding address components {value!r}")
    return parsed


def latency_summary(latencies):
    if not latencies:
        raise ValueError("at least one latency sample is required")
    ordered = sorted(latencies)
    p95 = ordered[math.ceil(len(ordered) * 0.95) - 1]
    return {
        "max_ms": max(latencies),
        "mean_ms": sum(latencies) / len(latencies),
        "p95_ms": p95,
    }


def validate_latency_budget(summary):
    violations = []
    if summary["p95_ms"] > P95_LATENCY_BUDGET_MS:
        violations.append(
            f"p95 {summary['p95_ms']:.1f} ms exceeds "
            f"{P95_LATENCY_BUDGET_MS:.0f} ms"
        )
    if summary["max_ms"] > MAX_LATENCY_BUDGET_MS:
        violations.append(
            f"maximum {summary['max_ms']:.1f} ms exceeds "
            f"{MAX_LATENCY_BUDGET_MS:.0f} ms"
        )
    if violations:
        raise RuntimeError("latency budget failed: " + "; ".join(violations))


def read_logs(stdout_path, stderr_path):
    return (
        stdout_path.read_text(encoding="utf-8", errors="replace"),
        stderr_path.read_text(encoding="utf-8", errors="replace"),
    )


def stop_server(process, shutdown_signal, stdout_path, stderr_path):
    if process.poll() is None:
        process.send_signal(shutdown_signal)
    try:
        process.wait(timeout=STOP_TIMEOUT)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)
        stdout, stderr = read_logs(stdout_path, stderr_path)
        raise RuntimeError(
            f"server did not stop after signal\nstdout:\n{stdout}\nstderr:\n{stderr}"
        )
    return read_logs(stdout_path, stderr_path)


def start_server(binary, context, directory):
    environment = os.environ.copy()
    environment["RSTREAM_CONTEXT"] = context
    environment["RSTREAM_TUNNEL_NAME"] = f"cpp-beast-q-{secrets.token_hex(4)}"
    stdout_path = directory / "stdout.log"
    stderr_path = directory / "stderr.log"
    with stdout_path.open("w", encoding="utf-8") as stdout_file:
        with stderr_path.open("w", encoding="utf-8") as stderr_file:
            process = subprocess.Popen(
                [binary],
                env=environment,
                stdout=stdout_file,
                stderr=stderr_file,
                text=True,
                start_new_session=True,
            )
    deadline = time.monotonic() + START_TIMEOUT
    consumed = 0
    try:
        while time.monotonic() < deadline:
            stdout, _ = read_logs(stdout_path, stderr_path)
            for line in stdout[consumed:].splitlines(keepends=True):
                if not line.endswith("\n"):
                    break
                consumed += len(line)
                if line.startswith("Forwarding address: "):
                    url = line.removeprefix("Forwarding address: ").strip()
                    parse_forwarding_url(url)
                    return process, url, stdout_path, stderr_path
            if process.poll() is not None:
                break
            time.sleep(0.05)
    except BaseException:
        if process.poll() is None:
            stop_server(process, signal.SIGTERM, stdout_path, stderr_path)
        raise
    stdout, stderr = stop_server(
        process,
        signal.SIGTERM,
        stdout_path,
        stderr_path,
    )
    raise RuntimeError(
        f"server did not publish a tunnel\nstdout:\n{stdout}\nstderr:\n{stderr}"
    )


def request(url, index):
    started = time.monotonic()
    with urllib.request.urlopen(f"{url}/runtime/{index}", timeout=15) as response:
        body = response.read()
        if response.status != 200:
            raise RuntimeError(f"unexpected HTTP status {response.status}")
        if body != EXPECTED_BODY:
            raise RuntimeError(f"unexpected response body {body!r}")
    return (time.monotonic() - started) * 1000


def validate_keep_alive(url):
    parsed = parse_forwarding_url(url)
    connection_type = (
        http.client.HTTPSConnection
        if parsed.scheme == "https"
        else http.client.HTTPConnection
    )
    connection = connection_type(parsed.hostname, parsed.port, timeout=15)
    try:
        connection.request("GET", "/runtime/keep-alive/one")
        first = connection.getresponse()
        if first.status != 200 or first.read() != EXPECTED_BODY:
            raise RuntimeError("first keep-alive response was invalid")
        transport = connection.sock
        if transport is None:
            raise RuntimeError("server closed a keep-alive response")
        connection.request("GET", "/runtime/keep-alive/two")
        second = connection.getresponse()
        if second.status != 200 or second.read() != EXPECTED_BODY:
            raise RuntimeError("second keep-alive response was invalid")
        if connection.sock is not transport:
            raise RuntimeError("second request used a new transport")
    finally:
        connection.close()


def open_partial_request(url):
    parsed = parse_forwarding_url(url)
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    connection = socket.create_connection((parsed.hostname, port), timeout=15)
    try:
        if parsed.scheme == "https":
            context = ssl.create_default_context()
            connection = context.wrap_socket(
                connection,
                server_hostname=parsed.hostname,
            )
        connection.sendall(
            f"GET /runtime/pending HTTP/1.1\r\nHost: {parsed.netloc}\r\n".encode()
        )
        connection.settimeout(0.5)
        try:
            received = connection.recv(1)
        except (TimeoutError, socket.timeout):
            received = None
        if received is not None:
            raise RuntimeError(
                "partial request did not remain blocked before shutdown"
            )
    except BaseException:
        connection.close()
        raise
    return connection


def validate_shutdown(binary, context, with_requests):
    with tempfile.TemporaryDirectory(prefix="rstream-beast-q-") as temporary:
        directory = Path(temporary)
        process, url, stdout_path, stderr_path = start_server(
            binary,
            context,
            directory,
        )
        pending = None
        operation_error = None
        latencies = []
        try:
            if with_requests:
                validate_keep_alive(url)
                with concurrent.futures.ThreadPoolExecutor(
                    max_workers=WORKER_COUNT
                ) as executor:
                    futures = [
                        executor.submit(request, url, index)
                        for index in range(REQUEST_COUNT)
                    ]
                    failures = []
                    for future in concurrent.futures.as_completed(futures):
                        try:
                            latencies.append(future.result())
                        except Exception as error:
                            failures.append(repr(error))
                    if failures:
                        raise RuntimeError(
                            f"{len(failures)} of {REQUEST_COUNT} concurrent "
                            "requests failed: "
                            + "; ".join(failures)
                        )
                pending = open_partial_request(url)
        except BaseException as error:
            operation_error = error
        try:
            stdout, stderr = stop_server(
                process,
                signal.SIGINT,
                stdout_path,
                stderr_path,
            )
        except BaseException as error:
            if operation_error is None:
                raise
            raise RuntimeError(
                f"runtime operation failed ({operation_error!r}) and shutdown "
                f"also failed ({error!r})"
            ) from error
        try:
            if process.returncode != 0:
                raise RuntimeError(
                    f"server returned {process.returncode} after SIGINT\n"
                    f"stdout:\n{stdout}\nstderr:\n{stderr}"
                )
            if operation_error is not None:
                raise RuntimeError(
                    f"runtime operation failed: {operation_error!r}\n"
                    f"stdout:\n{stdout}\nstderr:\n{stderr}"
                ) from operation_error
            if "fatal error:" in stderr or "session error:" in stderr:
                raise RuntimeError(
                    "server logged a runtime error despite a zero exit code\n"
                    f"stdout:\n{stdout}\nstderr:\n{stderr}"
                )
            if latencies:
                summary = latency_summary(latencies)
                validate_latency_budget(summary)
                print(
                    f"requests={REQUEST_COUNT} "
                    f"max_ms={summary['max_ms']:.1f} "
                    f"p95_ms={summary['p95_ms']:.1f} "
                    f"mean_ms={summary['mean_ms']:.1f}"
                )
        finally:
            if pending is not None:
                pending.close()
            if process.poll() is None:
                stop_server(
                    process,
                    signal.SIGKILL,
                    stdout_path,
                    stderr_path,
                )
        return latencies


def main():
    context = os.environ.get("RSTREAM_RUNTIME_CONTEXT")
    if not context:
        raise RuntimeError("RSTREAM_RUNTIME_CONTEXT is required")
    binary = os.environ.get(
        "RSTREAM_RUNTIME_BINARY",
        "out/bin/cpp_beast_rstream_tunnel",
    )
    validate_shutdown(binary, context, False)
    validate_shutdown(binary, context, True)
    print(
        f"PASS: idle shutdown, persistent HTTP, {REQUEST_COUNT} requests "
        f"with {WORKER_COUNT} workers, "
        "and shutdown with a pending read passed"
    )


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)
