from __future__ import annotations

import unittest

import cv2
import numpy as np

from images import encode_for_worker


class ImageEncodingTest(unittest.TestCase):
    def test_large_frame_is_resized_and_scale_is_preserved(self) -> None:
        frame = np.zeros((720, 1280, 3), dtype=np.uint8)
        payload, scale = encode_for_worker(frame, 640, "jpeg", 80)
        decoded = cv2.imdecode(np.frombuffer(payload, dtype=np.uint8), cv2.IMREAD_COLOR)
        self.assertIsNotNone(decoded)
        self.assertEqual(decoded.shape[:2], (360, 640))
        self.assertEqual(scale, 2.0)

    def test_small_frame_is_not_upscaled(self) -> None:
        frame = np.zeros((240, 320, 3), dtype=np.uint8)
        payload, scale = encode_for_worker(frame, 640, "png", 80)
        decoded = cv2.imdecode(np.frombuffer(payload, dtype=np.uint8), cv2.IMREAD_COLOR)
        self.assertIsNotNone(decoded)
        self.assertEqual(decoded.shape[:2], (240, 320))
        self.assertEqual(scale, 1.0)

    def test_unknown_codec_is_rejected(self) -> None:
        with self.assertRaisesRegex(ValueError, "unsupported frame codec"):
            encode_for_worker(np.zeros((10, 10, 3), dtype=np.uint8), 640, "gif", 80)

    def test_invalid_geometry_and_options_are_rejected(self) -> None:
        valid = np.zeros((32, 32, 3), dtype=np.uint8)
        invalid = (
            (np.array([], dtype=np.uint8), 640, "jpeg", 80),
            (valid, 0, "jpeg", 80),
            (valid, True, "jpeg", 80),
            (valid, 640, "jpeg", 0),
            (valid, 640, "jpeg", True),
        )
        for frame, input_size, codec, quality in invalid:
            with self.subTest(
                shape=frame.shape,
                input_size=input_size,
                codec=codec,
                quality=quality,
            ):
                with self.assertRaises(ValueError):
                    encode_for_worker(frame, input_size, codec, quality)


if __name__ == "__main__":
    unittest.main()
