import unittest

from analyze_rtp import align_ranges, analyze, intersection_count


def report(ranges, *, ssrc=42, duplicates=0, out_of_order=0, malformed=0):
    return {
        "malformedRtpPackets": malformed,
        "streams": [
            {
                "ssrc": ssrc,
                "sequenceRanges": ranges,
                "duplicates": duplicates,
                "outOfOrderArrivals": out_of_order,
                "maxReorderDistancePackets": 2,
                "timestampRegressions": 0,
            }
        ],
    }


class AnalyzeRTPTest(unittest.TestCase):
    def test_counts_range_intersection_without_expanding_packets(self):
        self.assertEqual(intersection_count([[1, 5], [9, 12]], [[3, 10]]), 5)

    def test_aligns_receiver_that_starts_after_sequence_wrap(self):
        self.assertEqual(align_ranges([[65530, 65540]], [[0, 4]]), [[65536, 65540]])

    def test_accepts_delivery_at_threshold(self):
        result = analyze(report([[1, 100]]), report([[1, 98]]), 98.0)
        self.assertTrue(result["passed"])
        self.assertEqual(result["receiver"]["missingSenderPackets"], 2)

    def test_rejects_unexpected_or_malformed_packets(self):
        unexpected = analyze(report([[1, 100]]), report([[1, 101]]), 99.0)
        malformed = analyze(report([[1, 100]]), report([[1, 100]], malformed=1), 99.0)
        self.assertFalse(unexpected["passed"])
        self.assertFalse(malformed["passed"])

    def test_rejects_different_streams(self):
        with self.assertRaises(ValueError):
            analyze(report([[1, 1]], ssrc=1), report([[1, 1]], ssrc=2), 99.0)


if __name__ == "__main__":
    unittest.main()
