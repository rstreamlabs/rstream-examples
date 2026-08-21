import assert from "node:assert/strict";
import test from "node:test";

import { negotiatedVideoCodecs } from "../lib/sdp-codecs.mjs";

test("reports only codecs retained by the negotiated video answer", () => {
  const codecs = negotiatedVideoCodecs(`v=0\r
m=video 9 UDP/TLS/RTP/SAVPF 96 97\r
a=rtpmap:96 H264/90000\r
a=rtpmap:97 rtx/90000\r
a=fmtp:97 apt=96\r
a=rtcp-fb:* transport-cc\r
a=rtcp-fb:96 nack\r
a=rtcp-fb:96 nack pli\r
a=rtpmap:118 flexfec-03/90000\r
m=audio 9 UDP/TLS/RTP/SAVPF 111\r
a=rtpmap:111 opus/48000/2\r
`);
  assert.deepEqual(
    codecs.map((codec) => codec.mimeType),
    ["video/H264", "video/rtx"],
  );
  assert.equal(codecs[1].sdpFmtpLine, "apt=96");
  assert.deepEqual(codecs[0].rtcpFeedback, [
    "transport-cc",
    "nack",
    "nack pli",
  ]);
  assert.deepEqual(codecs[1].rtcpFeedback, ["transport-cc"]);
});

test("rejects disabled video and ignores offered capabilities outside the answer", () => {
  assert.deepEqual(
    negotiatedVideoCodecs(
      "m=video 0 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 H264/90000\r\n",
    ),
    [],
  );
  assert.deepEqual(negotiatedVideoCodecs(""), []);
});

test("does not confuse an audio codec that reuses a video payload type", () => {
  const codecs = negotiatedVideoCodecs(`v=0\r
m=audio 9 UDP/TLS/RTP/SAVPF 96\r
a=rtpmap:96 opus/48000/2\r
m=video 9 UDP/TLS/RTP/SAVPF 96\r
a=rtpmap:96 H264/90000\r
`);
  assert.deepEqual(
    codecs.map((codec) => codec.mimeType),
    ["video/H264"],
  );
});

test("collects codecs from each active video media section", () => {
  const codecs = negotiatedVideoCodecs(`v=0\r
m=video 0 UDP/TLS/RTP/SAVPF 96\r
a=rtpmap:96 VP8/90000\r
m=video 9 UDP/TLS/RTP/SAVPF 102\r
a=rtpmap:102 H264/90000\r
`);
  assert.deepEqual(
    codecs.map((codec) => codec.mimeType),
    ["video/H264"],
  );
});

test("keeps format parameters scoped when video sections reuse payload types", () => {
  const codecs = negotiatedVideoCodecs(`v=0\r
m=video 9 UDP/TLS/RTP/SAVPF 96\r
a=rtpmap:96 H264/90000\r
a=fmtp:96 profile-level-id=42e01f\r
m=video 9 UDP/TLS/RTP/SAVPF 96\r
a=rtpmap:96 VP8/90000\r
`);
  assert.deepEqual(
    codecs.map((codec) => codec.sdpFmtpLine),
    ["profile-level-id=42e01f", ""],
  );
});

test("keeps RTCP feedback inside its media section and negotiated payload", () => {
  const codecs = negotiatedVideoCodecs(`v=0\r
m=audio 9 UDP/TLS/RTP/SAVPF 96\r
a=rtcp-fb:96 nack\r
a=rtpmap:96 opus/48000/2\r
m=video 9 UDP/TLS/RTP/SAVPF 102\r
a=rtpmap:102 H264/90000\r
a=rtcp-fb:96 nack\r
a=rtcp-fb:102 transport-cc\r
`);
  assert.deepEqual(codecs[0].rtcpFeedback, ["transport-cc"]);
});
