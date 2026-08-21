export function negotiatedVideoCodecs(sdp) {
  const lines = String(sdp || "").split(/\r?\n/);
  const codecs = [];
  for (let index = 0; index < lines.length; index += 1) {
    if (!lines[index].startsWith("m=video ")) {
      continue;
    }
    const fields = lines[index].trim().split(/\s+/);
    if (fields[1] === "0") {
      continue;
    }
    const payloads = new Set(fields.slice(3));
    const formats = new Map();
    const feedback = new Map();
    const wildcardFeedback = [];
    const sectionCodecs = [];
    for (
      index += 1;
      index < lines.length && !lines[index].startsWith("m=");
      index += 1
    ) {
      const line = lines[index].trim();
      const format = /^a=fmtp:(\d+)\s+(.+)$/i.exec(line);
      if (format && payloads.has(format[1])) {
        formats.set(format[1], format[2]);
        continue;
      }
      const rtcpFeedback = /^a=rtcp-fb:(\*|\d+)\s+([^\s]+)(?:\s+(.+))?$/i.exec(
        line,
      );
      if (
        rtcpFeedback &&
        (rtcpFeedback[1] === "*" || payloads.has(rtcpFeedback[1]))
      ) {
        const value = [rtcpFeedback[2], rtcpFeedback[3]]
          .filter(Boolean)
          .join(" ")
          .toLowerCase();
        if (rtcpFeedback[1] === "*") {
          wildcardFeedback.push(value);
        } else {
          const values = feedback.get(rtcpFeedback[1]) || [];
          values.push(value);
          feedback.set(rtcpFeedback[1], values);
        }
        continue;
      }
      const codec = /^a=rtpmap:(\d+)\s+([^/\s]+)\/(\d+)(?:\/(\d+))?$/i.exec(
        line,
      );
      if (!codec || !payloads.has(codec[1])) {
        continue;
      }
      sectionCodecs.push({
        channels: codec[4] ? Number(codec[4]) : null,
        clockRate: Number(codec[3]),
        mimeType: `video/${codec[2]}`,
        payloadType: Number(codec[1]),
        rtcpFeedback: [],
        sdpFmtpLine: "",
      });
    }
    for (const codec of sectionCodecs) {
      codec.sdpFmtpLine = formats.get(String(codec.payloadType)) || "";
      codec.rtcpFeedback = [
        ...wildcardFeedback,
        ...(feedback.get(String(codec.payloadType)) || []),
      ];
    }
    codecs.push(...sectionCodecs);
    index -= 1;
  }
  return codecs;
}
