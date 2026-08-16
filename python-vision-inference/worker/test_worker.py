from __future__ import annotations

import asyncio
import json
import math
import os
import threading
import unittest
from pathlib import Path
from unittest.mock import patch

import worker
from shared.protocol import read_message


class MemoryStream:
    def __init__(self, incoming: bytes = b"") -> None:
        self.reader = asyncio.StreamReader()
        self.reader.feed_data(incoming)
        self.reader.feed_eof()
        self.written = bytearray()
        self.closed = False

    async def __aenter__(self) -> MemoryStream:
        return self

    async def __aexit__(self, *_args: object) -> None:
        self.close()
        await self.wait_closed()

    async def readexactly(self, count: int) -> bytes:
        return await self.reader.readexactly(count)

    def write(self, data: bytes) -> None:
        self.written.extend(data)

    async def drain(self) -> None:
        return

    def close(self) -> None:
        self.closed = True

    async def wait_closed(self) -> None:
        return


def framed(header: dict[str, object], payload: bytes = b"") -> bytes:
    header_bytes = json.dumps(header).encode()
    total = 4 + len(header_bytes) + len(payload)
    return (
        total.to_bytes(4, "big")
        + len(header_bytes).to_bytes(4, "big")
        + header_bytes
        + payload
    )


def config() -> worker.WorkerConfig:
    return worker.WorkerConfig(
        name="worker-test",
        model="test-model",
        input_size=640,
        conf=0.4,
        device="cpu",
        accelerator="test-cpu",
    )


class WorkerValidationTest(unittest.TestCase):
    def test_default_name_does_not_disclose_a_hostname(self) -> None:
        with patch.object(worker.secrets, "token_hex", return_value="deadbeef"):
            self.assertEqual(worker._default_worker_name(), "worker-deadbeef")

    def test_frame_contract_rejects_ambiguous_or_unsafe_values(self) -> None:
        invalid = [
            ({"type": "other", "frame_id": 1, "codec": "jpeg"}, b"image"),
            ({"type": "frame", "frame_id": True, "codec": "jpeg"}, b"image"),
            ({"type": "frame", "frame_id": -1, "codec": "jpeg"}, b"image"),
            ({"type": "frame", "frame_id": 1 << 63, "codec": "jpeg"}, b"image"),
            ({"type": "frame", "frame_id": 1, "codec": "gif"}, b"image"),
            ({"type": "frame", "frame_id": 1, "codec": "jpeg"}, b""),
        ]
        for header, payload in invalid:
            with self.subTest(header=header):
                with self.assertRaises(worker.InvalidFrameError):
                    worker._validate_frame(header, payload)

    def test_invalid_encoded_image_is_not_reported_as_empty_detection(self) -> None:
        with self.assertRaisesRegex(worker.InvalidFrameError, "encoded image"):
            worker._detect(object(), b"not-an-image", 640, 0.4, "cpu")


class WorkerAsyncTest(unittest.IsolatedAsyncioTestCase):
    async def test_admission_is_bounded_under_concurrent_acquisition(self) -> None:
        admission = worker.SessionAdmission(4)
        results = await asyncio.gather(*(admission.try_acquire() for _ in range(100)))
        accepted = [result for result in results if result is not None]
        self.assertEqual(accepted, [0, 1, 2, 3])
        self.assertEqual(await admission.active(), 4)
        for _ in accepted:
            await admission.release()
        self.assertEqual(await admission.active(), 0)
        with self.assertRaisesRegex(RuntimeError, "without acquisition"):
            await admission.release()

    async def test_cancel_keeps_model_locked_until_thread_stops(self) -> None:
        repetitions = int(os.environ.get("VISION_STRESS_REPETITIONS", "1"))
        self.assertGreaterEqual(repetitions, 1)
        self.assertLessEqual(repetitions, 1_000)
        for repetition in range(repetitions):
            with self.subTest(repetition=repetition):
                await self._assert_cancel_keeps_model_locked()

    async def _assert_cancel_keeps_model_locked(self) -> None:
        first_started = threading.Event()
        second_started = threading.Event()
        release_first = threading.Event()
        invocation_lock = threading.Lock()
        invocations = 0

        def blocking_detect(*_args: object) -> tuple[list[dict[str, object]], float]:
            nonlocal invocations
            with invocation_lock:
                invocations += 1
                current = invocations
            if current == 1:
                first_started.set()
                if not release_first.wait(2):
                    raise TimeoutError("test did not release first inference")
            else:
                second_started.set()
            return [], 1.0

        inference = worker.InferenceRunner(object(), config())
        with patch.object(worker, "_detect", side_effect=blocking_detect):
            first = asyncio.create_task(inference.run(b"first"))
            self.assertTrue(await asyncio.to_thread(first_started.wait, 1))
            first.cancel()
            second = asyncio.create_task(inference.run(b"second"))
            await asyncio.sleep(0.05)
            self.assertFalse(first.done())
            self.assertFalse(second_started.is_set())
            release_first.set()
            with self.assertRaises(asyncio.CancelledError):
                await first
            self.assertEqual(await asyncio.wait_for(second, 1), ([], 1.0))
            self.assertTrue(second_started.is_set())

    async def test_invalid_frame_returns_protocol_error_and_releases_slot(self) -> None:
        incoming = framed(
            {"type": "frame", "frame_id": 7, "codec": "jpeg"},
            b"not-an-image",
        )
        stream = MemoryStream(incoming)
        admission = worker.SessionAdmission(1)
        already_serving = await admission.try_acquire()
        self.assertEqual(already_serving, 0)
        inference = worker.InferenceRunner(object(), config())
        await worker.serve_session(
            stream,
            inference,
            admission,
            already_serving,
            "eu-west-3",
        )
        self.assertEqual(await admission.active(), 0)
        output = MemoryStream(bytes(stream.written))
        hello = await read_message(output)
        error = await read_message(output)
        self.assertEqual(hello[0]["type"], "hello")
        self.assertEqual(hello[0]["max_sessions"], 1)
        self.assertEqual(hello[0]["engine_region"], "eu-west-3")
        self.assertEqual(error[0]["type"], "error")
        self.assertEqual(error[0]["code"], "invalid_frame")
        self.assertTrue(stream.closed)

    async def test_valid_frame_returns_result_and_releases_slot(self) -> None:
        incoming = framed(
            {"type": "frame", "frame_id": 19, "codec": "jpeg"},
            b"encoded-image",
        )
        stream = MemoryStream(incoming)
        admission = worker.SessionAdmission(2)
        already_serving = await admission.try_acquire()
        inference = worker.InferenceRunner(object(), config())
        detections = [
            {
                "box": [1.0, 2.0, 3.0, 4.0],
                "label": "car",
                "confidence": 0.9,
            }
        ]
        with patch.object(worker, "_detect", return_value=(detections, 2.34)):
            await worker.serve_session(
                stream,
                inference,
                admission,
                already_serving,
                "eu-west-3",
            )
        self.assertEqual(await admission.active(), 0)
        output = MemoryStream(bytes(stream.written))
        hello = await read_message(output)
        result = await read_message(output)
        self.assertEqual(hello[0]["type"], "hello")
        self.assertEqual(result[0]["type"], "result")
        self.assertEqual(result[0]["frame_id"], 19)
        self.assertEqual(result[0]["infer_ms"], 2.3)
        self.assertEqual(result[0]["detections"], detections)

    async def test_excess_session_gets_bounded_explicit_rejection(self) -> None:
        stream = MemoryStream()
        await worker.reject_session(stream)
        output = await read_message(MemoryStream(bytes(stream.written)))
        self.assertEqual(output[0]["type"], "error")
        self.assertEqual(output[0]["code"], "at_capacity")
        self.assertTrue(stream.closed)

    async def test_idle_session_is_closed_and_releases_its_slot(self) -> None:
        stream = MemoryStream()
        stream.reader = asyncio.StreamReader()
        admission = worker.SessionAdmission(1)
        already_serving = await admission.try_acquire()
        with patch.object(worker, "SESSION_IDLE_TIMEOUT", 0.01):
            await worker.serve_session(
                stream,
                worker.InferenceRunner(object(), config()),
                admission,
                already_serving,
                "eu-west-3",
            )
        self.assertEqual(await admission.active(), 0)
        output = MemoryStream(bytes(stream.written))
        self.assertEqual((await read_message(output))[0]["type"], "hello")
        error = await read_message(output)
        self.assertEqual(error[0]["code"], "idle_timeout")
        self.assertTrue(stream.closed)


@unittest.skipUnless(
    os.environ.get("MODEL") and os.environ.get("QUALIFICATION_MEDIA"),
    "MODEL and QUALIFICATION_MEDIA are required for real-model tests",
)
class RealModelTest(unittest.IsolatedAsyncioTestCase):
    async def test_real_model_handles_concurrent_reference_frames(self) -> None:
        model_path = Path(os.environ["MODEL"])
        media_path = Path(os.environ["QUALIFICATION_MEDIA"])
        self.assertTrue(model_path.is_file())
        self.assertTrue(media_path.is_file())
        frame = worker.cv2.imread(str(media_path))
        if frame is None:
            capture = worker.cv2.VideoCapture(str(media_path))
            try:
                ok, frame = capture.read()
            finally:
                capture.release()
            self.assertTrue(ok, "qualification media has no decodable first frame")
        ok, encoded = worker.cv2.imencode(".jpg", frame)
        self.assertTrue(ok)
        test_config = config()
        test_config = worker.WorkerConfig(
            name=test_config.name,
            model=str(model_path),
            input_size=test_config.input_size,
            conf=test_config.conf,
            device=test_config.device,
            accelerator=test_config.accelerator,
        )
        model = worker.YOLO(str(model_path))
        inference = worker.InferenceRunner(model, test_config)
        results = await asyncio.gather(
            *(inference.run(encoded.tobytes()) for _ in range(8))
        )
        for detections, infer_ms in results:
            self.assertIsInstance(detections, list)
            self.assertGreater(len(detections), 0)
            self.assertTrue(math.isfinite(infer_ms))
            self.assertGreater(infer_ms, 0)


if __name__ == "__main__":
    unittest.main()
