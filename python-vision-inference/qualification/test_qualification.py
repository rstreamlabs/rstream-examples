from __future__ import annotations

import argparse
import asyncio
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import live_helpers
from benchmark_model import percentile, validate_args
from live_helpers import (
    before_deadline,
    detection_signature,
    loopback_ready_path,
    stop_process_group,
    threshold_violations,
)
from render_report import render
from run import (
    count_personal_data,
    redact_personal_data,
    repository_state,
    untracked_fingerprint,
)
from transport_probe import MAX_ECHO_SIZE, validate_echo_size


class BenchmarkHelpersTest(unittest.TestCase):
    def test_percentile_uses_nearest_rank(self) -> None:
        self.assertEqual(percentile([1, 2, 3, 4], 0.50), 2)
        self.assertEqual(percentile([1, 2, 3, 4], 0.95), 4)

    def test_arguments_reject_unbounded_work(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "asset"
            path.write_bytes(b"x")
            args = argparse.Namespace(
                model=path,
                media=path,
                runs=0,
                concurrency=1,
                imgsz=640,
                conf=0.4,
                codec="jpeg",
                quality=80,
                max_p95_ms=0,
                min_throughput_fps=0,
            )
            with self.assertRaisesRegex(ValueError, "runs"):
                validate_args(args)

    def test_transport_probe_rejects_unbounded_echo_frames(self) -> None:
        validate_echo_size(1)
        validate_echo_size(MAX_ECHO_SIZE)
        for size in (0, MAX_ECHO_SIZE + 1):
            with self.subTest(size=size):
                with self.assertRaisesRegex(ValueError, "echo size"):
                    validate_echo_size(size)


class LiveQualificationHelpersTest(unittest.TestCase):
    def test_detection_signature_covers_boxes_scores_and_labels(self) -> None:
        baseline = [
            {"box": [1.0, 2.0, 3.0, 4.0], "confidence": 0.9, "label": "car"},
            {"box": [5.0, 6.0, 7.0, 8.0], "confidence": 0.8, "label": "bus"},
        ]
        self.assertEqual(
            detection_signature(baseline),
            detection_signature(list(reversed(baseline))),
        )
        for changed in (
            [{**baseline[0], "box": [1.0, 2.0, 3.1, 4.0]}, baseline[1]],
            [{**baseline[0], "confidence": 0.89}, baseline[1]],
            [{**baseline[0], "label": "truck"}, baseline[1]],
        ):
            with self.subTest(changed=changed):
                self.assertNotEqual(
                    detection_signature(baseline),
                    detection_signature(changed),
                )

    def test_personal_profile_paths_are_removed_from_artifacts(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "worker.log"
            artifact.write_text(
                "model=/Users/private-owner/models/yolo.pt "
                "host=Darwin workstation.local\n",
                encoding="utf-8",
            )
            self.assertEqual(redact_personal_data(root), 2)
            self.assertEqual(count_personal_data(root), 0)
            text = artifact.read_text(encoding="utf-8")
            self.assertNotIn("private-owner", text)
            self.assertNotIn("workstation.local", text)

    def test_child_rendezvous_path_is_absolute_for_relative_output(self) -> None:
        path = loopback_ready_path(Path("results/live.json"), "run")
        self.assertTrue(path.is_absolute())
        self.assertEqual(path.name, "vision-loopback-run.ready")

    def test_every_exceeded_budget_is_reported(self) -> None:
        args = argparse.Namespace(
            max_rtt_p95_ms=100,
            max_failover_ms=500,
            max_transport_overhead_p95_ms=50,
        )
        violations = threshold_violations(
            args,
            rstream_p95=175,
            failover_ms=750,
            loopback_p95=25,
        )
        self.assertEqual(len(violations), 3)
        self.assertTrue(any("RTT p95" in violation for violation in violations))
        self.assertTrue(any("failover" in violation for violation in violations))
        self.assertTrue(
            any("transport overhead" in violation for violation in violations)
        )

    def test_disabled_budgets_do_not_create_false_failures(self) -> None:
        args = argparse.Namespace(
            max_rtt_p95_ms=0,
            max_failover_ms=0,
            max_transport_overhead_p95_ms=0,
        )
        self.assertEqual(
            threshold_violations(
                args,
                rstream_p95=10_000,
                failover_ms=10_000,
                loopback_p95=1,
            ),
            [],
        )

    def test_repository_state_includes_untracked_content(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = root / "first.txt"
            second = root / "second.txt"
            first.write_text("one", encoding="utf-8")
            second.write_text("two", encoding="utf-8")
            with patch("run.REPOSITORY", root):
                original = untracked_fingerprint(b"first.txt\0second.txt\0")
                first.write_text("changed", encoding="utf-8")
                changed = untracked_fingerprint(b"first.txt\0second.txt\0")
            self.assertNotEqual(original, changed)

    def test_repository_state_fails_closed_when_git_fails(self) -> None:
        with patch("run.git_output", side_effect=RuntimeError("failed")):
            with self.assertRaisesRegex(RuntimeError, "failed"):
                repository_state()


class ProcessLifecycleTest(unittest.IsolatedAsyncioTestCase):
    async def test_exit_race_is_joined_after_process_group_disappears(self) -> None:
        class Process:
            pid = 123

            def __init__(self) -> None:
                self.wait_calls = 0

            def poll(self) -> None:
                return None

            def wait(self) -> int:
                self.wait_calls += 1
                return 0

        process = Process()
        with patch.object(
            live_helpers.os,
            "killpg",
            side_effect=ProcessLookupError,
        ) as killpg:
            await stop_process_group(process, abrupt=False, timeout=1)
        killpg.assert_called_once_with(process.pid, live_helpers.signal.SIGTERM)
        self.assertEqual(process.wait_calls, 1)

    async def test_hanging_operation_cannot_exceed_global_deadline(self) -> None:
        async def never_finishes() -> object:
            await asyncio.Future()
            raise AssertionError("unreachable")

        deadline = asyncio.get_running_loop().time() + 0.01
        with self.assertRaises(TimeoutError):
            await before_deadline(deadline, never_finishes)


class RenderReportTest(unittest.TestCase):
    def test_renders_passing_model_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = {
                "repository": {"revision": "abc", "dirty": False},
                "model": {"name": "model.pt", "sha256": "a" * 64},
                "media": {"name": "media.mp4", "sha256": "b" * 64},
            }
            benchmark = {
                "device": "cpu",
                "accelerator": "test",
                "payloadBytes": 42,
                "detectionCount": 2,
                "inferenceMS": {"min": 1, "p50": 2, "p95": 3, "max": 4},
                "sequentialThroughputFPS": 20,
                "concurrentThroughputFPS": 19,
                "violations": [],
            }
            session = {
                "manifest": manifest,
                "commands": [
                    {
                        "name": "tests",
                        "exitCode": 0,
                        "wallSeconds": 1.5,
                        "log": "tests.log",
                    }
                ],
                "secretScanFindings": 0,
                "privacyRedactions": 2,
                "personalDataFindings": 0,
            }
            (root / "model-benchmark.json").write_text(
                json.dumps(benchmark),
                encoding="utf-8",
            )
            (root / "live-mesh.json").write_text(
                json.dumps(
                    {
                        "workerEngineRegions": {"worker": "eu-west-3"},
                        "referencePayloadBytes": 42,
                        "frames": 5,
                        "loopback": {"roundTripP95MS": 3},
                        "roundTripMS": {"p50": 20, "p95": 30},
                        "capacityRejectionMS": 10,
                        "capacityRecoveryMS": 11,
                        "failureDetectionMS": 12,
                        "failoverMS": 25,
                        "violations": [],
                    }
                ),
                encoding="utf-8",
            )
            (root / "regional-routing.json").write_text(
                json.dumps(
                    {
                        "legacyChoice": "remote",
                        "latencyAwareChoice": "local",
                        "measuredMedianRTTGainMS": 80,
                        "observations": {
                            "remote": {
                                "engineRegion": "us-east-1",
                                "establishmentMS": 200,
                                "roundTripP50MS": 120,
                                "roundTripP95MS": 150,
                            },
                            "local": {
                                "engineRegion": "eu-west-3",
                                "establishmentMS": 80,
                                "roundTripP50MS": 40,
                                "roundTripP95MS": 60,
                            },
                        },
                        "violations": [],
                    }
                ),
                encoding="utf-8",
            )
            session_path = root / "session.json"
            session_path.write_text(json.dumps(session), encoding="utf-8")
            self.assertTrue(render(session_path))
            report = (root / "report.md").read_text(encoding="utf-8")
            self.assertIn("Verdict: PASS", report)
            self.assertIn("Inference p95", report)
            self.assertIn("Live mesh and failure handling", report)
            self.assertIn("Regional worker selection", report)
            self.assertIn("performance observational", report)
            self.assertIn("not an SLO pass", report)
            self.assertFalse((root / "commands.svg").exists())
            self.assertTrue((root / "model-latency.svg").is_file())
            self.assertTrue((root / "failover-timeline.svg").is_file())
            self.assertTrue((root / "regional-routing.svg").is_file())
            timeline = (root / "failover-timeline.svg").read_text(encoding="utf-8")
            self.assertIn("EOF 12.0 ms", timeline)
            self.assertIn("worker B result 25.0 ms", timeline)

    def test_missing_benchmark_fails(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            session = {
                "manifest": {
                    "repository": {"revision": "abc", "dirty": True},
                    "model": {"name": "model.pt", "sha256": "a" * 64},
                    "media": {"name": "media.mp4", "sha256": "b" * 64},
                },
                "commands": [],
                "secretScanFindings": 0,
            }
            path = root / "session.json"
            path.write_text(json.dumps(session), encoding="utf-8")
            self.assertFalse(render(path))
            report = (root / "report.md").read_text(encoding="utf-8")
            self.assertIn("did not produce", report)


if __name__ == "__main__":
    unittest.main()
