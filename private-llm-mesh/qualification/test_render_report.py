import json
import tempfile
import unittest
from pathlib import Path

from render_report import render


class RenderReportTest(unittest.TestCase):
    def test_renders_local_pass(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            session = {
                "manifest": {
                    "repository": {"revision": "abc", "dirty": False},
                    "model": {"name": "model.gguf", "bytes": 42, "sha256": "f" * 64},
                },
                "commands": [
                    {"name": "tests", "exitCode": 0, "wallSeconds": 1.5, "log": "tests.log"}
                ],
                "liveEvidence": None,
                "secretScanFindings": 0,
            }
            path = root / "session.json"
            path.write_text(json.dumps(session), encoding="utf-8")
            self.assertTrue(render(path))
            report = (root / "report.md").read_text(encoding="utf-8")
            self.assertIn("Verdict: PASS", report)
            self.assertNotIn("Raw log", report)
            self.assertNotIn("tests.log", report)
            self.assertFalse((root / "commands.svg").exists())

    def test_renders_worker_lifecycle_from_ordered_turns(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            thresholds = {"maxWorkerShare": 0.75}
            phases = {
                "live": {
                    "generatedAt": "2026-08-16T14:00:10Z",
                    "wallSeconds": 10,
                    "parameters": {"thresholds": thresholds},
                    "summary": {
                        "successful": 2,
                        "turns": 2,
                        "workerCount": 2,
                        "turnsPerSecond": 1,
                        "ttftP50MS": 10,
                        "ttftP95MS": 12,
                        "totalP50MS": 11,
                        "totalP95MS": 13,
                        "workers": {"qualification-worker-a": 1, "qualification-worker-b": 1},
                        "maxWorkerShare": 0.5,
                    },
                    "violations": [],
                    "results": [
                        {"ok": True, "worker": "qualification-worker-a"},
                        {"ok": True, "worker": "qualification-worker-b"},
                    ],
                },
                "degraded": {
                    "generatedAt": "2026-08-16T14:00:25Z",
                    "wallSeconds": 5,
                    "parameters": {"thresholds": {"maxWorkerShare": 1}},
                    "summary": {
                        "successful": 1,
                        "turns": 1,
                        "workerCount": 1,
                        "maxWorkerShare": 1,
                        "workers": {"qualification-worker-b": 1},
                    },
                    "violations": [],
                    "results": [{"ok": True, "worker": "qualification-worker-b"}],
                },
                "recovery": {
                    "generatedAt": "2026-08-16T14:00:40Z",
                    "wallSeconds": 5,
                    "parameters": {"thresholds": thresholds},
                    "summary": {
                        "successful": 2,
                        "turns": 2,
                        "workerCount": 2,
                        "maxWorkerShare": 0.5,
                        "workers": {"qualification-worker-a": 1, "qualification-worker-b": 1},
                    },
                    "violations": [],
                    "results": [
                        {"ok": True, "worker": "qualification-worker-b"},
                        {"ok": True, "worker": "qualification-worker-a"},
                    ],
                },
            }
            for name, phase in phases.items():
                (root / f"{name}.json").write_text(json.dumps(phase), encoding="utf-8")
            session = {
                "manifest": {
                    "repository": {"revision": "abc", "dirty": False},
                    "model": {"name": "model.gguf", "bytes": 42, "sha256": "f" * 64},
                },
                "commands": [],
                "liveEvidence": "live.json",
                "secretScanFindings": 0,
            }
            path = root / "session.json"
            path.write_text(json.dumps(session), encoding="utf-8")
            self.assertTrue(render(path))
            report = (root / "report.md").read_text(encoding="utf-8")
            self.assertIn("Routing through worker loss and recovery", report)
            chart = (root / "worker-lifecycle.svg").read_text(encoding="utf-8")
            self.assertIn("A stopped", chart)
            self.assertIn("A returned", chart)
            self.assertIn("Elapsed time", chart)
            self.assertIn("controlled worker loss and recovery", chart)

    def test_live_violation_fails(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            summary = {
                "successful": 3,
                "turns": 4,
                "workerCount": 1,
                "turnsPerSecond": 1.0,
                "ttftP50MS": None,
                "ttftP95MS": None,
                "totalP50MS": 20,
                "totalP95MS": 30,
                "workers": {"worker-a": 3},
            }
            (root / "live.json").write_text(
                json.dumps({"violations": ["skew"], "summary": summary}),
                encoding="utf-8",
            )
            session = {
                "manifest": {
                    "repository": {"revision": "abc", "dirty": True},
                    "model": {"name": "model.gguf", "bytes": 42, "sha256": "f" * 64},
                },
                "commands": [],
                "liveEvidence": "live.json",
                "secretScanFindings": 0,
            }
            path = root / "session.json"
            path.write_text(json.dumps(session), encoding="utf-8")
            self.assertFalse(render(path))
            report = (root / "report.md").read_text(encoding="utf-8")
            self.assertIn("Verdict: FAIL", report)
            self.assertIn("Live threshold: skew", report)


if __name__ == "__main__":
    unittest.main()
