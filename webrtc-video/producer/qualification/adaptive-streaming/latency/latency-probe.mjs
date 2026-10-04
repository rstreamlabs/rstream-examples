import { decodeMarker } from "./decode-marker.mjs";

// Qualification page only. Requires producer and browser on one Linux host.
// This measures a raw-frame marker to expected browser composition, not a
// physical camera's exposure or a monitor's actual scanout.
export function installLatencyProbe(video, sampleIntervalMilliseconds = 200) {
  if (!video?.requestVideoFrameCallback || !video?.cancelVideoFrameCallback)
    throw new TypeError(
      "Frame callbacks are required for latency qualification",
    );
  if (
    !Number.isFinite(sampleIntervalMilliseconds) ||
    sampleIntervalMilliseconds < 100
  )
    throw new TypeError("Latency sampling interval must be at least 100ms");
  const canvas = document.createElement("canvas");
  canvas.width = 256;
  canvas.height = 32;
  const context = canvas.getContext("2d", {
    willReadFrequently: true,
    alpha: false,
  });
  if (!context) throw new Error("Canvas context unavailable");
  let stopped = false,
    callbackID,
    nextSample = 0;
  let samples = [],
    rejected = 0,
    omitted = 0,
    maximumProbeMilliseconds = 0;
  const onFrame = (now, metadata) => {
    if (stopped) return;
    if (now >= nextSample) {
      nextSample = now + sampleIntervalMilliseconds;
      const started = performance.now();
      try {
        if (
          metadata.width < 288 ||
          metadata.height < 64 ||
          !Number.isFinite(metadata.expectedDisplayTime)
        ) {
          rejected++;
        } else {
          context.drawImage(video, 16, 16, 256, 32, 0, 0, 256, 32);
          const marker = decodeMarker(context.getImageData(0, 0, 256, 32).data);
          const sampledClockOffset =
            Date.now() - (performance.timeOrigin + performance.now());
          if (!marker) {
            rejected++;
          } else {
            const presentation =
              performance.timeOrigin + metadata.expectedDisplayTime;
            const sample = {
              sourceUnixMilliseconds: marker.timestampMilliseconds,
              expectedDisplayUnixMilliseconds: presentation,
              latencyMilliseconds: presentation - marker.timestampMilliseconds,
              callbackLatenessMilliseconds: now - metadata.expectedDisplayTime,
              sampledClockOffsetMilliseconds: sampledClockOffset,
              rtpTimestamp: Number.isFinite(metadata.rtpTimestamp)
                ? metadata.rtpTimestamp
                : null,
              mediaTimeSeconds: metadata.mediaTime,
              width: metadata.width,
              height: metadata.height,
              minimumContrast: marker.minimumContrast,
            };
            if (samples.length < 32) samples.push(sample);
            else omitted++;
          }
        }
      } catch {
        rejected++;
      } finally {
        maximumProbeMilliseconds = Math.max(
          maximumProbeMilliseconds,
          performance.now() - started,
        );
      }
    }
    callbackID = video.requestVideoFrameCallback(onFrame);
  };
  callbackID = video.requestVideoFrameCallback(onFrame);
  return {
    read() {
      const snapshot = { samples, rejected, omitted, maximumProbeMilliseconds };
      samples = [];
      rejected = omitted = maximumProbeMilliseconds = 0;
      return snapshot;
    },
    stop() {
      if (stopped) return;
      stopped = true;
      video.cancelVideoFrameCallback(callbackID);
      canvas.width = canvas.height = 0;
    },
  };
}
