import pathlib
import tempfile
import unittest

from compare_frames import analyze, longest_common_subsequence


class CompareFramesTest(unittest.TestCase):
    def test_lcs_aligns_omitted_frames(self):
        self.assertEqual(longest_common_subsequence([b"a", b"b", b"c"], [b"a", b"c"]), 2)

    def test_reports_missing_and_altered_frames(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            reference = root / "reference.raw"
            candidate = root / "candidate.raw"
            reference.write_bytes(b"aaaabbbbccccdddd")
            candidate.write_bytes(b"aaaaxxxxdddd")
            result = analyze(reference, candidate, 4, 4, 50)
        self.assertTrue(result["passed"])
        self.assertEqual(result["identicalFramesInOrder"], 2)
        self.assertEqual(result["missingOrAlteredReferenceFrames"], 2)
        self.assertEqual(result["alteredCandidateFrames"], 1)

    def test_rejects_partial_and_wrong_reference(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            reference = root / "reference.raw"
            candidate = root / "candidate.raw"
            reference.write_bytes(b"aaaabbbb")
            candidate.write_bytes(b"bad")
            with self.assertRaisesRegex(ValueError, "not a multiple"):
                analyze(reference, candidate, 4, 2, 100)
            candidate.write_bytes(b"aaaa")
            with self.assertRaisesRegex(ValueError, "reference contains"):
                analyze(reference, candidate, 4, 3, 100)


if __name__ == "__main__":
    unittest.main()
