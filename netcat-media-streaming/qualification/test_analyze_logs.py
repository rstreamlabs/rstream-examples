import pathlib
import tempfile
import unittest

from analyze_logs import RULES, analyze


class AnalyzeLogsTest(unittest.TestCase):
    def test_accepts_bounded_known_warning(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "decoder.stderr").write_text(
                "WARN GST_POLL teardown: couldn't find fd !\n",
                encoding="utf-8",
            )
            result = analyze(root)
        self.assertTrue(result["passed"])
        self.assertEqual(result["acceptedWarnings"]["gstreamer_fd_teardown"], 1)

    def test_rejects_unknown_warning(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "server.log").write_text(
                "WARN unexpected transport degradation\n",
                encoding="utf-8",
            )
            result = analyze(root)
        self.assertFalse(result["passed"])
        self.assertEqual(len(result["unknownSeverities"]), 1)

    def test_rejects_known_warning_above_ceiling(self):
        maximum = next(maximum for name, _, maximum in RULES if name == "gstreamer_fd_teardown")
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "decoder.stderr").write_text(
                "WARN GST_POLL teardown: couldn't find fd !\n" * (maximum + 1),
                encoding="utf-8",
            )
            result = analyze(root)
        self.assertFalse(result["passed"])
        self.assertEqual(result["exceededWarningCeilings"][0]["count"], maximum + 1)


if __name__ == "__main__":
    unittest.main()
