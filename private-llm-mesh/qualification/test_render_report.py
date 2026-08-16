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
            self.assertIn("Verdict: PASS", (root / "report.md").read_text(encoding="utf-8"))
            self.assertIn("Qualification phase duration", (root / "commands.svg").read_text(encoding="utf-8"))

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
