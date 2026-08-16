from __future__ import annotations

import asyncio
import math
import threading
import time
import unittest
from unittest.mock import patch

import device


def hello(name: str) -> dict[str, object]:
    return {
        "type": "hello",
        "worker": name,
        "model": "yolov8n.pt",
        "input_size": 640,
        "codecs": ["jpeg", "webp", "png"],
        "engine_region": "eu-west-3",
        "active_sessions": 0,
        "max_sessions": 4,
    }


class DeviceStateTest(unittest.TestCase):
    def test_rendezvous_hashing_distributes_and_minimizes_remapping(self) -> None:
        original = ["worker-a", "worker-b", "worker-c", "worker-d"]
        expanded = [*original, "worker-e"]
        original_counts = {worker: 0 for worker in original}
        for index in range(1000):
            state = device.DeviceState()
            state.device_name = f"device-{index}"
            state.workers = {worker: "online" for worker in original}
            previous = state.pick_workers(1)[0]
            original_counts[previous] += 1
            state.workers = {worker: "online" for worker in expanded}
            current = state.pick_workers(1)[0]
            if current != previous:
                self.assertEqual(current, "worker-e")
        for count in original_counts.values():
            self.assertGreater(count, 200)
            self.assertLess(count, 300)

    def test_pin_is_advisory_and_falls_back_during_cooldown(self) -> None:
        state = device.DeviceState()
        state.workers = {"pinned": "online", "fallback": "online"}
        state.pinned_worker = "pinned"
        self.assertEqual(state.select_candidates(), ["pinned"])
        state.cooldown["pinned"] = time.monotonic() + 60
        self.assertEqual(state.select_candidates(), ["fallback"])

    def test_worker_events_never_keep_offline_or_malformed_entries(self) -> None:
        state = device.DeviceState()
        state.workers = {"worker": "online"}
        state.worker_labels = {"worker": {"role": "inference"}}
        self.assertTrue(
            device._apply_worker_event(
                state,
                "tunnel.updated",
                {"name": "worker", "status": "offline", "labels": {}},
            )
        )
        self.assertNotIn("worker", state.workers)
        self.assertFalse(
            device._apply_worker_event(
                state,
                "tunnel.updated",
                {"name": "invalid", "status": "online", "labels": "bad"},
            )
        )
        self.assertNotIn("invalid", state.workers)

    def test_hello_validation_rejects_unsafe_worker_contracts(self) -> None:
        invalid = [
            {**hello("worker"), "worker": ""},
            {**hello("worker"), "input_size": 0},
            {**hello("worker"), "input_size": True},
            {**hello("worker"), "active_sessions": -1},
            {**hello("worker"), "active_sessions": "busy"},
            {**hello("worker"), "max_sessions": 0},
            {**hello("worker"), "max_sessions": True},
            {**hello("worker"), "active_sessions": 4},
            {**hello("worker"), "codecs": []},
            {**hello("worker"), "codecs": ["jpeg", 1]},
            {**hello("worker"), "engine_region": ""},
            {**hello("worker"), "engine_region": 3},
        ]
        for header in invalid:
            with self.subTest(header=header):
                with self.assertRaises(ValueError):
                    device._validate_hello(header)

    def test_session_load_is_normalized_by_worker_capacity(self) -> None:
        small = {**hello("small"), "active_sessions": 1, "max_sessions": 2}
        large = {**hello("large"), "active_sessions": 1, "max_sessions": 8}
        self.assertGreater(device._session_load(small), device._session_load(large))

    def test_equal_load_is_broken_by_connection_establishment_time(self) -> None:
        header = hello("worker")
        self.assertLess(
            device._candidate_score(header, 20),
            device._candidate_score(header, 200),
        )

    def test_capacity_remains_more_important_than_latency(self) -> None:
        idle = {**hello("idle"), "active_sessions": 0}
        busy = {**hello("busy"), "active_sessions": 1}
        self.assertLess(
            device._candidate_score(idle, 500),
            device._candidate_score(busy, 1),
        )

    def test_worker_errors_are_safe_and_actionable(self) -> None:
        self.assertEqual(
            device._worker_error(
                {"type": "error", "code": "at_capacity", "message": "full"}
            ),
            "worker error at_capacity: full",
        )
        self.assertEqual(
            device._worker_error({}),
            "worker error worker_error: request failed",
        )

    def test_result_contract_rejects_malformed_or_non_finite_data(self) -> None:
        valid = {
            "type": "result",
            "frame_id": 7,
            "infer_ms": 2.5,
            "detections": [
                {
                    "box": [1.0, 2.0, 3.0, 4.0],
                    "label": "car",
                    "confidence": 0.9,
                }
            ],
        }
        self.assertEqual(
            device._parse_result(valid, b""),
            (7, 2.5, valid["detections"]),
        )
        invalid = [
            ({**valid, "frame_id": True}, b""),
            ({**valid, "infer_ms": math.nan}, b""),
            ({**valid, "detections": "bad"}, b""),
            ({**valid, "detections": [{"box": [0, 0, -1, 1]}]}, b""),
            (
                {
                    **valid,
                    "detections": [
                        {
                            "box": [0, 0, 1, 1],
                            "label": "car",
                            "confidence": 2,
                        }
                    ],
                },
                b"",
            ),
            (valid, b"unexpected"),
        ]
        for header, payload in invalid:
            with self.subTest(header=header, payload=payload):
                with self.assertRaises(ValueError):
                    device._parse_result(header, payload)

    def test_device_options_have_safe_operational_bounds(self) -> None:
        device._validate_options(0, 1)
        device._validate_options(240, 100)
        for fps, quality in ((-1, 80), (math.nan, 80), (241, 80), (30, 0), (30, 101)):
            with self.subTest(fps=fps, quality=quality):
                with self.assertRaises(ValueError):
                    device._validate_options(fps, quality)

    def test_control_payloads_reject_ambiguous_coercions(self) -> None:
        self.assertFalse(device._detection_enabled({"enabled": False}))
        for payload in ({}, {"enabled": "false"}, {"enabled": 0}):
            with self.subTest(payload=payload):
                with self.assertRaisesRegex(ValueError, "boolean"):
                    device._detection_enabled(payload)
        self.assertIsNone(device._worker_pin({"worker": None}))
        self.assertEqual(device._worker_pin({"worker": "worker-a"}), "worker-a")
        for payload in ({}, {"worker": ""}, {"worker": 1}, {"worker": "x" * 129}):
            with self.subTest(payload=payload):
                with self.assertRaisesRegex(ValueError, "worker"):
                    device._worker_pin(payload)


class DeviceAsyncTest(unittest.IsolatedAsyncioTestCase):
    async def test_cancel_joins_frame_encode_before_returning(self) -> None:
        started = threading.Event()
        finish = threading.Event()

        def blocking_encode(*_args: object) -> tuple[bytes, float]:
            started.set()
            if not finish.wait(2):
                raise TimeoutError("test did not release encoder")
            return b"encoded", 1.0

        frame = device.np.zeros((32, 32, 3), dtype=device.np.uint8)
        with patch.object(device, "_encode_for_worker", side_effect=blocking_encode):
            task = asyncio.create_task(
                device._encode_for_worker_async(frame, 640, "jpeg", 80)
            )
            self.assertTrue(await asyncio.to_thread(started.wait, 1))
            task.cancel()
            await asyncio.sleep(0.05)
            self.assertFalse(task.done())
            finish.set()
            with self.assertRaises(asyncio.CancelledError):
                await task

    async def test_duplicate_result_cannot_expand_inflight_window(self) -> None:
        slots = asyncio.Semaphore(1)
        await slots.acquire()
        in_flight = {7: (1.0, 1.0)}
        self.assertEqual(device._complete_frame(in_flight, slots, 7), (1.0, 1.0))
        await slots.acquire()
        with self.assertRaisesRegex(ValueError, "unknown frame_id"):
            device._complete_frame(in_flight, slots, 7)
        self.assertTrue(slots.locked())

    async def test_candidate_probes_start_concurrently(self) -> None:
        state = device.DeviceState()
        started: set[str] = set()
        both_started = asyncio.Event()
        release = asyncio.Event()

        async def fake_open(
            _client: object, name: str
        ) -> tuple[object, dict[str, object]]:
            started.add(name)
            if len(started) == 2:
                both_started.set()
            await release.wait()
            return object(), hello(name)

        with patch.object(device, "open_session", side_effect=fake_open):
            task = asyncio.create_task(
                device._open_candidates(state, object(), ["worker-a", "worker-b"])
            )
            await asyncio.wait_for(both_started.wait(), 1)
            release.set()
            opened = await task
        self.assertEqual([entry[0] for entry in opened], ["worker-a", "worker-b"])

    async def test_failed_registry_seed_preserves_last_known_good_pool(self) -> None:
        state = device.DeviceState()
        state.workers = {"known": "online"}
        state.worker_labels = {"known": {"role": "inference"}}

        class FailingClient:
            async def list_tunnels(self, **_kwargs: object) -> object:
                raise ConnectionError("registry unavailable")

        async def stop_backoff(_delay: float) -> None:
            raise asyncio.CancelledError

        with patch.object(device.asyncio, "sleep", side_effect=stop_backoff):
            with self.assertRaises(asyncio.CancelledError):
                await device.registry_loop(state, FailingClient())
        self.assertEqual(state.workers, {"known": "online"})
        self.assertEqual(state.worker_labels, {"known": {"role": "inference"}})

    async def test_failed_candidate_is_cooled_while_healthy_peer_remains(self) -> None:
        state = device.DeviceState()

        async def fake_open(
            _client: object, name: str
        ) -> tuple[object, dict[str, object]]:
            if name == "bad":
                raise ConnectionError("dial failed")
            return object(), hello(name)

        with patch.object(device, "open_session", side_effect=fake_open):
            opened = await device._open_candidates(state, object(), ["bad", "good"])
        self.assertEqual([entry[0] for entry in opened], ["good"])
        self.assertGreater(state.cooldown["bad"], time.monotonic())

    async def test_sender_failure_ends_session_without_waiting_for_response_timeout(
        self,
    ) -> None:
        class SendFailStream:
            async def readexactly(self, _count: int) -> bytes:
                await asyncio.Future()
                raise AssertionError("unreachable")

            def write(self, _data: bytes) -> None:
                return

            async def drain(self) -> None:
                raise OSError("send failed")

        state = device.DeviceState()
        state.add_frame(device.np.zeros((32, 32, 3), dtype=device.np.uint8))
        with self.assertRaisesRegex(OSError, "send failed"):
            await asyncio.wait_for(
                device.run_session(
                    state,
                    SendFailStream(),
                    hello("worker"),
                    "jpeg",
                    80,
                ),
                1,
            )

    async def test_capture_cancel_joins_read_before_camera_release(self) -> None:
        started = threading.Event()
        finish = threading.Event()
        released = threading.Event()

        class BlockingCapture:
            def isOpened(self) -> bool:
                return True

            def read(self) -> tuple[bool, object]:
                started.set()
                if not finish.wait(2):
                    raise TimeoutError("test did not release capture")
                return True, device.np.zeros((32, 32, 3), dtype=device.np.uint8)

            def release(self) -> None:
                released.set()

        capture = BlockingCapture()
        with patch.object(device.cv2, "VideoCapture", return_value=capture):
            task = asyncio.create_task(
                device.capture_loop(device.DeviceState(), "0", 30)
            )
            self.assertTrue(await asyncio.to_thread(started.wait, 1))
            task.cancel()
            await asyncio.sleep(0.05)
            self.assertFalse(task.done())
            self.assertFalse(released.is_set())
            finish.set()
            with self.assertRaises(asyncio.CancelledError):
                await task
        self.assertTrue(released.is_set())


if __name__ == "__main__":
    unittest.main()
