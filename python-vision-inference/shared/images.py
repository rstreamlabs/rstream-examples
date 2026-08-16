"""Image encoding shared by the device and its qualification tools."""

from __future__ import annotations

import cv2
import numpy as np

CODECS = {"jpeg": ".jpg", "webp": ".webp", "png": ".png"}
MAX_INPUT_SIZE = 8192


def encode_for_worker(
    frame: np.ndarray,
    input_size: int,
    codec: str,
    quality: int,
) -> tuple[bytes, float]:
    """Encode one frame exactly as the live device sends it to a worker."""
    if codec not in CODECS:
        raise ValueError(f"unsupported frame codec {codec!r}")
    if not isinstance(frame, np.ndarray) or frame.ndim != 3 or frame.size == 0:
        raise ValueError("frame must be a non-empty HxWxC array")
    if (
        not isinstance(input_size, int)
        or isinstance(input_size, bool)
        or not 32 <= input_size <= MAX_INPUT_SIZE
    ):
        raise ValueError(f"input_size must be an integer in [32,{MAX_INPUT_SIZE}]")
    if (
        not isinstance(quality, int)
        or isinstance(quality, bool)
        or not 1 <= quality <= 100
    ):
        raise ValueError("quality must be an integer in [1,100]")
    height, width = frame.shape[:2]
    scale = max(width, height) / float(input_size)
    if scale > 1.0:
        frame = cv2.resize(frame, (round(width / scale), round(height / scale)))
    else:
        scale = 1.0
    params: list[int] = []
    if codec == "jpeg":
        params = [cv2.IMWRITE_JPEG_QUALITY, quality]
    elif codec == "webp":
        params = [cv2.IMWRITE_WEBP_QUALITY, quality]
    ok, encoded = cv2.imencode(CODECS[codec], frame, params)
    if not ok:
        raise ValueError(f"failed to encode frame with codec {codec}")
    return encoded.tobytes(), scale
