import tempfile
import unittest
from pathlib import Path

from run import (
    AVERAGE_LATENCY_BUDGET_MS,
    MINIMUM_TPS,
    evaluate,
    parse_pgbench,
    parse_pgbench_logs,
    percentile,
    render_report,
    render_svg,
)


class QualificationTest(unittest.TestCase):
    def measurements(self):
        return {
            "tls": True,
            "rollbackRows": 0,
            "committedRows": 1,
            "bulk": {
                "verified": True,
                "rows": 10_000,
                "bytes": 5_120_000,
                "bytesPerSecond": 250_000,
            },
            "load": {
                "averageLatencyMS": AVERAGE_LATENCY_BUDGET_MS,
                "p95LatencyMS": 750,
                "maximumLatencyMS": 2_000,
                "transactionsPerSecond": MINIMUM_TPS,
            },
            "cancellationSeconds": 5.0,
            "recoverySeconds": {
                "database": 20.0,
                "publisher": 10.0,
                "client listener": 1.0,
            },
        }

    def test_percentile_uses_nearest_rank(self):
        self.assertEqual(percentile([5, 1, 4, 2, 3], 0.95), 5)
        with self.assertRaises(ValueError):
            percentile([], 0.95)

    def test_parse_pgbench_requires_both_measurements(self):
        output = "latency average = 12.345 ms\ntps = 648.123456 (without initial connection time)\n"
        self.assertEqual(
            parse_pgbench(output),
            {"averageLatencyMS": 12.345, "transactionsPerSecond": 648.123456},
        )
        with self.assertRaises(RuntimeError):
            parse_pgbench("tps = 1.0 (without initial connection time)")

    def test_parse_pgbench_transaction_logs(self):
        with tempfile.TemporaryDirectory() as directory:
            first = Path(directory) / "pgbench_log.1"
            second = Path(directory) / "pgbench_log.2"
            first.write_text("0 1 1000 0 1 1\n0 2 3000 0 1 1\n", encoding="utf-8")
            second.write_text("1 1 2000 0 1 1\n1 2 4000 0 1 1\n", encoding="utf-8")
            self.assertEqual(
                parse_pgbench_logs([first, second], 4),
                {
                    "p50LatencyMS": 2,
                    "p95LatencyMS": 4,
                    "p99LatencyMS": 4,
                    "maximumLatencyMS": 4,
                },
            )
            with self.assertRaises(RuntimeError):
                parse_pgbench_logs([first], 4)

    def test_exact_budgets_pass(self):
        checks = evaluate(self.measurements())
        self.assertTrue(all(check["passed"] for check in checks))

    def test_each_regression_fails_its_gate(self):
        cases = (
            ("tls", False, "postgres-tls"),
            ("rollbackRows", 1, "transaction-rollback"),
            ("committedRows", 2, "transaction-commit"),
            ("cancellationSeconds", 5.001, "query-cancellation"),
        )
        for field, value, expected in cases:
            with self.subTest(field=field):
                measurements = self.measurements()
                measurements[field] = value
                failed = [
                    check["name"]
                    for check in evaluate(measurements)
                    if not check["passed"]
                ]
                self.assertIn(expected, failed)

    def test_load_and_recovery_regressions_fail(self):
        measurements = self.measurements()
        measurements["load"] = {
            "averageLatencyMS": AVERAGE_LATENCY_BUDGET_MS + 0.1,
            "p95LatencyMS": 750.1,
            "maximumLatencyMS": 2_000.1,
            "transactionsPerSecond": MINIMUM_TPS - 0.1,
        }
        measurements["recoverySeconds"]["database"] = 20.1
        failed = {
            check["name"] for check in evaluate(measurements) if not check["passed"]
        }
        self.assertEqual(
            failed,
            {
                "concurrent-throughput",
                "concurrent-latency",
                "concurrent-tail-latency",
                "database-recovery",
            },
        )

    def test_reports_render_without_runtime_paths(self):
        measurements = self.measurements()
        checks = evaluate(measurements)
        manifest = {"repository": {"revision": "0123456789abcdef"}}
        report = render_report(manifest, measurements, checks)
        svg = render_svg(measurements["recoverySeconds"])
        self.assertIn("qualification — PASS", report)
        self.assertIn("database", svg)
        self.assertNotIn(str(Path.home()), report + svg)

    def test_rendered_artifacts_are_plain_utf8(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.md"
            measurements = self.measurements()
            path.write_text(
                render_report(
                    {"repository": {"revision": "revision"}},
                    measurements,
                    evaluate(measurements),
                ),
                encoding="utf-8",
            )
            self.assertTrue(path.read_text(encoding="utf-8").endswith("\n"))


if __name__ == "__main__":
    unittest.main()
