import json
import pathlib
import tempfile
import unittest

from render_report import collect, render_markdown


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
                        "receiver": {
                            "deliveryPercent": 99.5,
                            "deliveredSenderPackets": 99,
                            "missingSenderPackets": 1,
                            "duplicates": 0,
                            "outOfOrderArrivals": 0,
                        },
                        "sender": {"uniquePackets": 100},
                        "thresholds": {"minimumPacketDeliveryPercent": 99},
                    }
                ),
                encoding="utf-8",
            )
            (scenario / "frame-comparison.json").write_text(
                json.dumps(
                    {
                        "identicalPercent": 98,
                        "identicalFramesInOrder": 98,
                        "referenceFrames": 100,
                        "minimumIdenticalPercent": 90,
                    }
                ),
                encoding="utf-8",
            )
            results = collect(root)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["revision"], "abc")
        self.assertEqual(results[0]["packetDeliveryPercent"], 99.5)
        self.assertEqual(results[0]["identicalFramesPercent"], 98)
        self.assertEqual(results[0]["deliveredPackets"], 99)
        self.assertEqual(results[0]["identicalFrames"], 98)

    def test_markdown_explains_the_validation_method(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "report.md"
            render_markdown(
                [
                    {
                        "scenario": "best-effort",
                        "summary": "PASS 300/300 frames",
                        "revision": "abc",
                        "dirty": False,
                    }
                ],
                output,
            )
            report = output.read_text(encoding="utf-8")
        self.assertIn("checked frame by frame", report)
        self.assertIn("PASS 300/300 frames", report)


if __name__ == "__main__":
    unittest.main()
