import pathlib
import tempfile
import unittest

from analyze_frames import analyze


class AnalyzeFramesTest(unittest.TestCase):
    def test_accepts_delivery_above_threshold_without_freezes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "frames.raw"
            path.write_bytes(b"aaaabbbbcccc")
            result = analyze(path, 4, 4, 75)
        self.assertTrue(result["passed"])
        self.assertEqual(result["decodedFrames"], 3)
        self.assertEqual(result["consecutiveDuplicates"], 0)

    def test_rejects_consecutive_duplicate_frames(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "frames.raw"
            path.write_bytes(b"aaaaaaaa")
            result = analyze(path, 4, 2, 100)
        self.assertFalse(result["passed"])
        self.assertEqual(result["consecutiveDuplicates"], 1)

    def test_rejects_partial_frame(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "frames.raw"
            path.write_bytes(b"partial")
            with self.assertRaisesRegex(ValueError, "not a multiple"):
                analyze(path, 4, 2, 100)


if __name__ == "__main__":
    unittest.main()
