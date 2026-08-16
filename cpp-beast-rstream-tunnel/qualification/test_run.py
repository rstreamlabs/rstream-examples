import tempfile
import unittest
from pathlib import Path

from run import (
    assert_private_data_absent,
    render_report,
    render_svg,
    require_clean_worktree,
)


class QualificationRenderingTest(unittest.TestCase):
    def test_renders_latency_svg_without_external_assets(self):
        svg = render_svg(
            [
                {"p95_ms": 500.0, "max_ms": 650.0, "mean_ms": 400.0},
                {"p95_ms": 600.0, "max_ms": 700.0, "mean_ms": 450.0},
            ]
        )
        self.assertIn("<svg", svg)
        self.assertIn("100% of budget", svg)
        self.assertIn("p95 / 2000 ms", svg)
        self.assertIn("maximum / 5000 ms", svg)
        self.assertNotIn("<image", svg)
        self.assertNotIn(" href=", svg)

    def test_report_states_scope_and_verdict(self):
        report = render_report(
            {
                "revision": "abc123",
                "campaign_count": 1,
                "campaigns": [
                    {"p95_ms": 500.0, "max_ms": 650.0, "mean_ms": 400.0}
                ],
                "request_count": 64,
                "requests_per_campaign": 64,
                "concurrency": 16,
                "aggregate": {
                    "p95_ms": 500.0,
                    "max_ms": 650.0,
                    "mean_ms": 400.0,
                },
                "budgets": {"p95_ms": 2_000.0, "max_ms": 5_000.0},
            }
        )
        self.assertIn("**Verdict: PASS**", report)
        self.assertIn("completed 1 campaign", report)
        self.assertIn("not a universal public SLO", report)

    def test_rejects_private_host_identifiers(self):
        with tempfile.TemporaryDirectory() as temporary:
            artifact = Path(temporary) / "artifact.txt"
            artifact.write_text(Path.home().name, encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "private host data"):
                assert_private_data_absent((artifact,))

    def test_refuses_dirty_reference_evidence_without_explicit_override(self):
        require_clean_worktree(False, False)
        require_clean_worktree(True, True)
        with self.assertRaisesRegex(ValueError, "worktree is dirty"):
            require_clean_worktree(True, False)


if __name__ == "__main__":
    unittest.main()
