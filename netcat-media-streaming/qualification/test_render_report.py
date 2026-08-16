import json
import pathlib
import tempfile
import unittest

from render_report import collect, render_bars


class RenderReportTest(unittest.TestCase):
    def test_collects_pinned_packet_and_quality_results(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            scenario = root / "datagram"
            scenario.mkdir()
            (scenario / "manifest.json").write_text(
                json.dumps(
                    {
                        "scenario": "best-effort",
                        "git": {"revision": "abc", "dirty": False},
                    }
                ),
                encoding="utf-8",
            )
            (scenario / "rtp-analysis.json").write_text(
                json.dumps(
                    {
                        "receiver": {"deliveryPercent": 99.5},
                        "thresholds": {"minimumPacketDeliveryPercent": 99},
                    }
                ),
                encoding="utf-8",
            )
            (scenario / "frame-comparison.json").write_text(
                json.dumps(
                    {"identicalPercent": 98, "minimumIdenticalPercent": 90}
                ),
                encoding="utf-8",
            )
            results = collect(root)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["revision"], "abc")
        self.assertEqual(results[0]["packetDeliveryPercent"], 99.5)
        self.assertEqual(results[0]["identicalFramesPercent"], 98)

    def test_svg_escapes_labels_and_marks_failed_bar_red(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "chart.svg"
            render_bars("A & B", [("unsafe <name>", 80, 90)], output)
            svg = output.read_text(encoding="utf-8")
        self.assertIn("A &amp; B", svg)
        self.assertIn("unsafe &lt;name&gt;", svg)
        self.assertIn("#ef4444", svg)


if __name__ == "__main__":
    unittest.main()
