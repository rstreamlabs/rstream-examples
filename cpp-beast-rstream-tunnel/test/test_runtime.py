import unittest

from runtime import latency_summary, parse_forwarding_url, validate_latency_budget


class RuntimeHelpersTest(unittest.TestCase):
    def test_accepts_http_origin_without_path_state(self):
        parsed = parse_forwarding_url("https://example.test:8443")
        self.assertEqual(parsed.hostname, "example.test")
        self.assertEqual(parsed.port, 8443)

    def test_rejects_unsafe_or_ambiguous_forwarding_addresses(self):
        for value in (
            "ftp://example.test",
            "https:///missing-host",
            "https://example.test/unexpected",
            "https://example.test?query=1",
            "https://example.test#fragment",
        ):
            with self.subTest(value=value):
                with self.assertRaises(ValueError):
                    parse_forwarding_url(value)

    def test_summarizes_latency_with_nearest_rank_percentile(self):
        summary = latency_summary([float(value) for value in range(1, 21)])
        self.assertEqual(summary["p95_ms"], 19.0)
        self.assertEqual(summary["max_ms"], 20.0)
        self.assertEqual(summary["mean_ms"], 10.5)

    def test_rejects_empty_latency_samples(self):
        with self.assertRaisesRegex(ValueError, "at least one"):
            latency_summary([])

    def test_enforces_tail_and_outlier_budgets(self):
        validate_latency_budget(
            {"p95_ms": 1_999.0, "max_ms": 4_999.0, "mean_ms": 100.0}
        )
        with self.assertRaisesRegex(RuntimeError, "p95"):
            validate_latency_budget(
                {"p95_ms": 2_001.0, "max_ms": 4_999.0, "mean_ms": 100.0}
            )
        with self.assertRaisesRegex(RuntimeError, "maximum"):
            validate_latency_budget(
                {"p95_ms": 1_999.0, "max_ms": 5_001.0, "mean_ms": 100.0}
            )


if __name__ == "__main__":
    unittest.main()
