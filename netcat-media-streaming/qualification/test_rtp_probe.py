import io
import struct
import unittest

from rtp_probe import RTPProbe, compress_ranges, copy_and_probe, extend_counter


def packet(sequence, timestamp=9000, ssrc=42, payload=b"payload"):
    header = struct.pack("!BBHII", 0x80, 96, sequence, timestamp, ssrc)
    return header + payload


def framed(*packets):
    return b"".join(struct.pack("!H", len(value)) + value for value in packets)


class RTPProbeTest(unittest.TestCase):
    def test_preserves_bytes_and_reports_loss_reordering_and_duplicate(self):
        source_bytes = framed(
            packet(65534, 100),
            packet(0, 300),
            packet(65535, 200),
            packet(0, 300),
            packet(2, 500),
        )
        source = io.BytesIO(source_bytes)
        destination = io.BytesIO()
        probe = RTPProbe()
        copy_and_probe(source, destination, probe)
        self.assertEqual(destination.getvalue(), source_bytes)
        stream = probe.report()["streams"][0]
        self.assertEqual(stream["sequenceRanges"], [[65534, 65536], [65538, 65538]])
        self.assertEqual(stream["missingWithinObservedRange"], 1)
        self.assertEqual(stream["duplicates"], 1)
        self.assertEqual(stream["outOfOrderArrivals"], 1)
        self.assertEqual(stream["maxReorderDistancePackets"], 1)

    def test_rejects_truncated_frame(self):
        source = io.BytesIO(b"\x00\x05abc")
        with self.assertRaises(EOFError):
            copy_and_probe(source, io.BytesIO(), RTPProbe())

    def test_counter_extension_and_range_compression(self):
        self.assertEqual(extend_counter(0, 65535, 16), 65536)
        self.assertEqual(extend_counter(65535, 65536, 16), 65535)
        self.assertEqual(compress_ranges({1, 2, 3, 7, 9, 10}), [[1, 3], [7, 7], [9, 10]])


if __name__ == "__main__":
    unittest.main()
