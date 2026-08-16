import { appendFile, mkdir, rename, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import process from "node:process";
import { chromium } from "playwright-core";
import { readPhase } from "./lib/phase.mjs";
import { PathStability, pathMatchesPolicy } from "./lib/path.mjs";

const argumentsByName = parseArguments(process.argv.slice(2));
const url = requiredArgument(argumentsByName, "url");
const outputDirectory = requiredArgument(argumentsByName, "output-directory");
const phaseFile = requiredArgument(argumentsByName, "phase-file");
const icePolicy = argumentsByName.get("ice-policy") || "relay";
if (!new Set(["direct", "relay"]).has(icePolicy)) {
  throw new Error(`ice-policy must be direct or relay, got ${icePolicy}`);
}
const browserSandbox = argumentsByName.get("browser-sandbox") || "enabled";
if (!new Set(["disabled", "enabled"]).has(browserSandbox)) {
  throw new Error(
    `browser-sandbox must be disabled or enabled, got ${browserSandbox}`,
  );
}
const maximumDurationSeconds = numberArgument(
  argumentsByName,
  "maximum-duration-seconds",
  300,
);
const sampleIntervalMilliseconds = numberArgument(
  argumentsByName,
  "sample-interval-milliseconds",
  1000,
);
const playoutDelayHintSeconds = boundedNumberArgument(
  argumentsByName,
  "playout-delay-hint-seconds",
  0,
  0,
  1,
);
const browserExecutable =
  argumentsByName.get("browser-executable") ||
  process.env.BROWSER_EXECUTABLE_PATH ||
  defaultBrowserExecutable();

await mkdir(outputDirectory, { recursive: true });
const samplesPath = `${outputDirectory}/samples.jsonl`;
const readyPath = `${outputDirectory}/collector-ready.json`;
const browserPath = `${outputDirectory}/browser.json`;
const signalingPath = `${outputDirectory}/signaling-events.json`;
const failurePath = `${outputDirectory}/collector-failure.json`;
let browser;
let page;

try {
  browser = await chromium.launch({
    executablePath: browserExecutable,
    headless: true,
    args: [
      "--autoplay-policy=no-user-gesture-required",
      ...(browserSandbox === "disabled" ? ["--no-sandbox"] : []),
    ],
  });
  const context = await browser.newContext({
    ignoreHTTPSErrors: false,
    viewport: { width: 1440, height: 900 },
  });
  await context.addInitScript(() => {
    const NativeRTCPeerConnection = window.RTCPeerConnection;
    const NativeWebSocket = window.WebSocket;
    const peers = [];
    const sockets = [];
    const telemetry = {
      events: [],
      iceRestartOffers: 0,
      lastOfferUfrag: "",
      localCandidatesSent: 0,
      offersSent: 0,
      remoteCandidatesReceived: 0,
      webSocketCloseCount: 0,
      webSocketOpenCount: 0,
    };
    const record = (kind, fields = {}) => {
      telemetry.events.push({
        elapsedMilliseconds: Math.round(performance.now()),
        kind,
        ...fields,
      });
    };
    class QualificationRTCPeerConnection extends NativeRTCPeerConnection {
      constructor(configuration, constraints) {
        super(configuration, constraints);
        peers.push(this);
        const peerID = peers.length;
        Object.defineProperty(this, "__rstreamQualificationID", {
          configurable: false,
          enumerable: false,
          value: peerID,
          writable: false,
        });
        record("peer-created", { peerID });
        for (const [eventName, stateName] of [
          ["connectionstatechange", "connectionState"],
          ["iceconnectionstatechange", "iceConnectionState"],
          ["icegatheringstatechange", "iceGatheringState"],
          ["signalingstatechange", "signalingState"],
        ]) {
          this.addEventListener(eventName, () => {
            record(eventName, { peerID, state: this[stateName] });
          });
        }
      }
    }
    Object.defineProperty(window, "__rstreamQualificationPeers", {
      configurable: false,
      enumerable: false,
      value: peers,
      writable: false,
    });
    Object.defineProperty(window, "__rstreamQualificationSockets", {
      configurable: false,
      enumerable: false,
      value: sockets,
      writable: false,
    });
    Object.defineProperty(window, "__rstreamQualificationTelemetry", {
      configurable: false,
      enumerable: false,
      value: telemetry,
      writable: false,
    });
    Object.defineProperty(window, "__rstreamQualificationSessionStats", {
      configurable: false,
      enumerable: false,
      value: { latest: null },
      writable: false,
    });
    class QualificationWebSocket extends NativeWebSocket {
      constructor(...args) {
        super(...args);
        sockets.push(this);
        const socketID = sockets.length;
        Object.defineProperty(this, "__rstreamQualificationID", {
          configurable: false,
          enumerable: false,
          value: socketID,
          writable: false,
        });
        record("websocket-created", { socketID });
        this.addEventListener("open", () => {
          telemetry.webSocketOpenCount += 1;
          record("websocket-open", { socketID });
        });
        this.addEventListener("close", (event) => {
          telemetry.webSocketCloseCount += 1;
          record("websocket-close", {
            code: event.code,
            clean: event.wasClean,
            socketID,
          });
        });
        this.addEventListener("message", (event) => {
          if (typeof event.data !== "string") {
            return;
          }
          try {
            const message = JSON.parse(event.data);
            if (message?.type === "session.stats" && message.stats) {
              window.__rstreamQualificationSessionStats.latest = message.stats;
            } else if (message?.type === "webrtc.candidate") {
              telemetry.remoteCandidatesReceived += 1;
            }
          } catch {
            // Non-JSON application frames are irrelevant to this collector.
          }
        });
      }

      send(data) {
        if (typeof data === "string") {
          try {
            const message = JSON.parse(data);
            if (message?.type === "webrtc.offer") {
              telemetry.offersSent += 1;
              const ufrag = /^a=ice-ufrag:(.+)$/m.exec(message.sdp || "")?.[1];
              if (
                ufrag &&
                telemetry.lastOfferUfrag &&
                ufrag !== telemetry.lastOfferUfrag
              ) {
                telemetry.iceRestartOffers += 1;
              }
              if (ufrag) {
                telemetry.lastOfferUfrag = ufrag;
              }
              record("offer-sent", {
                iceGeneration: telemetry.iceRestartOffers + 1,
                socketID: this.__rstreamQualificationID,
              });
            } else if (message?.type === "webrtc.candidate") {
              telemetry.localCandidatesSent += 1;
            }
          } catch {
            // Non-JSON application frames are irrelevant to this collector.
          }
        }
        super.send(data);
      }
    }
    window.RTCPeerConnection = QualificationRTCPeerConnection;
    window.WebSocket = QualificationWebSocket;
  });
  page = await context.newPage();
  page.on("console", (message) => {
    if (message.type() === "error") {
      const location = message.location();
      const source = location.url
        ? ` (${location.url}:${location.lineNumber || 0}:${location.columnNumber || 0})`
        : "";
      process.stderr.write(
        `browser console error: ${message.text()}${source}\n`,
      );
    }
  });
  page.on("pageerror", (error) => {
    process.stderr.write(`browser page error: ${error.message}\n`);
  });
  page.on("requestfailed", (request) => {
    process.stderr.write(
      `browser request failed: ${request.method()} ${request.url()} (${request.failure()?.errorText || "unknown error"})\n`,
    );
  });
  page.on("response", (response) => {
    if (response.status() >= 400) {
      process.stderr.write(
        `browser response error: ${response.status()} ${response.request().method()} ${response.url()}\n`,
      );
    }
  });
  await navigateWithRetries(page, url, 3);
  await page.waitForFunction(
    () => {
      const button = document.querySelector("#connect");
      return button instanceof HTMLButtonElement && !button.disabled;
    },
    undefined,
    { timeout: 60_000 },
  );
  await page.selectOption("#turn-policy", icePolicy);
  await page.click("#connect");
  await page.waitForFunction(
    () => {
      const peers = window.__rstreamQualificationPeers || [];
      const peer = peers.at(-1);
      return Boolean(
        peer
          ?.getReceivers()
          .some((receiver) => receiver.track?.kind === "video"),
      );
    },
    undefined,
    { timeout: 30_000 },
  );
  if (playoutDelayHintSeconds > 0) {
    await page.evaluate((hintSeconds) => {
      const peers = window.__rstreamQualificationPeers || [];
      const peer = peers.at(-1);
      const receiver = peer
        ?.getReceivers()
        .find((candidate) => candidate.track?.kind === "video");
      if (!receiver || !("playoutDelayHint" in receiver)) {
        throw new Error(
          "Chromium does not expose RTCRtpReceiver.playoutDelayHint",
        );
      }
      receiver.playoutDelayHint = hintSeconds;
      if (receiver.playoutDelayHint !== hintSeconds) {
        throw new Error(
          "Chromium rejected the requested receiver playout delay",
        );
      }
    }, playoutDelayHintSeconds);
  }
  await page.waitForFunction(
    () =>
      document.querySelector("#peer-status")?.textContent ===
        "Peer: connected" &&
      document.querySelector("#playback-status")?.textContent === "Playing",
    undefined,
    { timeout: 90_000 },
  );
  let initialSample = null;
  let pathStable = false;
  const pathStability = new PathStability(3000);
  const pathDeadline = performance.now() + 30_000;
  while (performance.now() < pathDeadline) {
    initialSample = await collectSample(page);
    if (
      pathMatchesPolicy(initialSample, icePolicy) &&
      pathStability.observe(initialSample, performance.now())
    ) {
      pathStable = true;
      break;
    }
    await delay(500);
  }
  if (!initialSample || !pathStable) {
    throw new Error(
      `${icePolicy} session did not stabilize on the required candidate path (${initialSample?.localCandidateType || "unknown"}/${initialSample?.remoteCandidateType || "unknown"})`,
    );
  }
  const browserVersion = await browser.version();
  await writeJSONAtomic(browserPath, {
    browserExecutable,
    browserSandbox,
    browserVersion,
    peerConnection: await collectPeerMetadata(page),
    userAgent: await page.evaluate(() => navigator.userAgent),
  });
  await writeJSONAtomic(readyPath, {
    connectedAt: new Date().toISOString(),
    icePolicy,
    mediaDestinationAddress: initialSample.localCandidateAddress,
    mediaDestinationPort: initialSample.localCandidatePort,
    mediaDestinationProtocol: initialSample.localCandidateProtocol,
    localCandidateType: initialSample.localCandidateType,
    remoteCandidateType: initialSample.remoteCandidateType,
    producerICEPath: {
      localCandidateAddress: initialSample.remoteCandidateAddress,
      localCandidateProtocol: initialSample.producerLocalCandidateProtocol,
      localCandidateType: initialSample.producerLocalCandidateType,
      localCandidateURL: initialSample.producerLocalCandidateURL,
      localRelayProtocol: initialSample.producerLocalRelayProtocol,
      remoteCandidateProtocol: initialSample.producerRemoteCandidateProtocol,
      remoteCandidateType: initialSample.producerRemoteCandidateType,
    },
  });
  const startedAt = performance.now();
  let disconnectedSince = null;
  while (performance.now() - startedAt < maximumDurationSeconds * 1000) {
    const phase = await readPhase(phaseFile);
    const sample = await collectSample(page);
    sample.capturedAt = new Date().toISOString();
    sample.elapsedMilliseconds = Math.round(performance.now() - startedAt);
    sample.phase = phase.name;
    sample.phaseStartedAt = phase.startedAt;
    await appendFile(samplesPath, `${JSON.stringify(sample)}\n`, "utf8");
    const connected =
      sample.peerConnectionState === "connected" &&
      sample.playback === "Playing";
    if (connected) {
      disconnectedSince = null;
    } else if (disconnectedSince === null) {
      disconnectedSince = performance.now();
    } else if (performance.now() - disconnectedSince > 15_000) {
      throw new Error(
        `stream remained unavailable for more than 15 seconds during ${phase.name}`,
      );
    }
    if (phase.name === "complete") {
      break;
    }
    await delay(sampleIntervalMilliseconds);
  }
  const finalPhase = await readPhase(phaseFile);
  if (finalPhase.name !== "complete") {
    throw new Error(
      `collector reached its ${maximumDurationSeconds}s safety deadline before the scenario completed`,
    );
  }
} catch (error) {
  const normalized = normalizeError(error);
  await writeJSONAtomic(failurePath, {
    failedAt: new Date().toISOString(),
    message: normalized.message,
    stack: normalized.stack,
  });
  throw normalized;
} finally {
  if (page) {
    await writeJSONAtomic(
      signalingPath,
      await collectSignalingMetadata(page),
    ).catch(() => {});
    await page.click("#disconnect").catch(() => {});
  }
  if (browser) {
    await browser.close().catch(() => {});
  }
}

async function collectSample(activePage) {
  return activePage.evaluate(async () => {
    const peers = window.__rstreamQualificationPeers || [];
    const peer = [...peers]
      .reverse()
      .find((candidate) => candidate.connectionState !== "closed");
    if (!peer) {
      throw new Error("the page has no active RTCPeerConnection");
    }
    const reports = await peer.getStats();
    const byID = new Map();
    for (const report of reports.values()) {
      byID.set(report.id, report);
    }
    const inboundReports = [];
    let selectedPair = null;
    for (const report of reports.values()) {
      if (
        report.type === "inbound-rtp" &&
        (report.kind === "video" || report.mediaType === "video")
      ) {
        inboundReports.push(report);
      }
      if (report.type === "transport" && report.selectedCandidatePairId) {
        selectedPair = byID.get(report.selectedCandidatePairId) || selectedPair;
      }
    }
    const inbound = inboundReports.sort(
      (left, right) => (right.bytesReceived || 0) - (left.bytesReceived || 0),
    )[0];
    if (!selectedPair) {
      for (const report of reports.values()) {
        if (
          report.type === "candidate-pair" &&
          report.state === "succeeded" &&
          (report.selected || report.nominated)
        ) {
          selectedPair = report;
          break;
        }
      }
    }
    const localCandidate = selectedPair
      ? byID.get(selectedPair.localCandidateId)
      : null;
    const remoteCandidate = selectedPair
      ? byID.get(selectedPair.remoteCandidateId)
      : null;
    const video = document.querySelector("#video");
    const videoReceiver = peer
      .getReceivers()
      .find((receiver) => receiver.track?.kind === "video");
    const sessionStats = window.__rstreamQualificationSessionStats?.latest;
    const telemetry = window.__rstreamQualificationTelemetry || {};
    const sockets = window.__rstreamQualificationSockets || [];
    const socket =
      [...sockets]
        .reverse()
        .find((candidate) => candidate.readyState === WebSocket.OPEN) ||
      sockets.at(-1);
    const bandwidth = sessionStats?.bandwidth;
    const text = (selector) =>
      document.querySelector(selector)?.textContent?.trim() || "";
    return {
      bytesReceived: inbound?.bytesReceived || 0,
      codecID: inbound?.codecId || "",
      currentRoundTripTimeSeconds: selectedPair?.currentRoundTripTime ?? null,
      decoderImplementation: inbound?.decoderImplementation || "",
      delayControllerState: bandwidth?.state || "",
      delayControllerUsage: bandwidth?.usage || "",
      delayEstimateMilliseconds: bandwidth?.delayEstimateMs ?? null,
      delayMeasurementMilliseconds: bandwidth?.delayMeasurementMs ?? null,
      delayTargetKbps: bandwidth?.delayTargetBitrateBps
        ? bandwidth.delayTargetBitrateBps / 1000
        : 0,
      delayThresholdMilliseconds: bandwidth?.delayThresholdMs ?? null,
      estimatedPlayoutTimestamp: inbound?.estimatedPlayoutTimestamp ?? null,
      framesDecoded: inbound?.framesDecoded || 0,
      framesDropped: inbound?.framesDropped || 0,
      framesPerSecond: inbound?.framesPerSecond || 0,
      frameHeight: inbound?.frameHeight || 0,
      frameWidth: inbound?.frameWidth || 0,
      freezeCount: inbound?.freezeCount || 0,
      jitterSeconds: inbound?.jitter ?? null,
      jitterBufferDelaySeconds: inbound?.jitterBufferDelay ?? null,
      jitterBufferEmittedCount: inbound?.jitterBufferEmittedCount ?? null,
      jitterBufferTargetDelaySeconds: inbound?.jitterBufferTargetDelay ?? null,
      localCandidateAddress:
        localCandidate?.address || localCandidate?.ip || "",
      localCandidatePort: localCandidate?.port || 0,
      localCandidateProtocol: localCandidate?.protocol || "",
      localCandidateType: localCandidate?.candidateType || "",
      lossAverage: bandwidth?.averageLoss ?? null,
      flexFECMediaPackets: bandwidth?.flexFECMediaPackets || 0,
      flexFECRepairPackets: bandwidth?.flexFECRepairPackets || 0,
      lossTargetKbps: bandwidth?.lossTargetBitrateBps
        ? bandwidth.lossTargetBitrateBps / 1000
        : 0,
      nackCount: inbound?.nackCount || 0,
      packetsDiscarded: inbound?.packetsDiscarded || 0,
      packetsLost: inbound?.packetsLost || 0,
      packetsReceived: inbound?.packetsReceived || 0,
      pacerTargetBitrateKbps: bandwidth?.pacerTargetBitrateBps
        ? bandwidth.pacerTargetBitrateBps / 1000
        : 0,
      pacerPacingBitrateKbps: bandwidth?.pacerPacingBitrateBps
        ? bandwidth.pacerPacingBitrateBps / 1000
        : 0,
      pacerQueueDrops: bandwidth?.pacerQueueDrops || 0,
      pacerQueueDelayMilliseconds: bandwidth?.pacerQueueDelayMs || 0,
      pacerMaximumQueueDelayMilliseconds: bandwidth?.pacerMaximumDelayMs || 0,
      pacerMaximumPrimaryDelayMilliseconds:
        bandwidth?.pacerMaximumPrimaryDelayMs || 0,
      pacerMaximumRepairDelayMilliseconds:
        bandwidth?.pacerMaximumRepairDelayMs || 0,
      pacerMaximumRTXDelayMilliseconds: bandwidth?.pacerMaximumRTXDelayMs || 0,
      pacerMaximumFECDelayMilliseconds: bandwidth?.pacerMaximumFECDelayMs || 0,
      pacerMaximumSustainedDelayMilliseconds:
        bandwidth?.pacerMaximumSustainedDelayMs || 0,
      pacerMaximumAdmittedDelayMilliseconds:
        bandwidth?.pacerMaximumAdmittedDelayMs || 0,
      pacerKeyFrameReserveBytes: bandwidth?.pacerKeyFrameReserveBytes || 0,
      pacerMediaFramesDropped: bandwidth?.pacerMediaFrameDrops || 0,
      pacerMediaBytesDropped: bandwidth?.pacerMediaByteDrops || 0,
      pacerRepairPacketsExpired: bandwidth?.pacerRepairPacketsExpired || 0,
      pacerRepairPacketsTrimmed: bandwidth?.pacerRepairPacketsTrimmed || 0,
      pacerRTXPacketsExpired: bandwidth?.pacerRTXPacketsExpired || 0,
      pacerFECPacketsExpired: bandwidth?.pacerFECPacketsExpired || 0,
      pacerRTXPacketsTrimmed: bandwidth?.pacerRTXPacketsTrimmed || 0,
      pacerFECPacketsTrimmed: bandwidth?.pacerFECPacketsTrimmed || 0,
      pacerQueuePackets: bandwidth?.pacerQueuePackets || 0,
      pacerSentPrimary: bandwidth?.pacerSentPrimary || 0,
      pacerSentRepair: bandwidth?.pacerSentRepair || 0,
      pacerSentRTX: bandwidth?.pacerSentRTX || 0,
      adaptiveBitrateUpdates: sessionStats?.adaptiveBitrateUpdates || 0,
      adaptiveBitrateFailures: sessionStats?.adaptiveBitrateFailures || 0,
      staleBitrateCallbacks: bandwidth?.staleBitrateCallbacks || 0,
      twccFeedbackPackets: bandwidth?.twccFeedbackPackets || 0,
      twccMalformedFeedback: bandwidth?.twccMalformedFeedback || 0,
      twccPaddingStatuses: bandwidth?.twccPaddingStatuses || 0,
      twccReportedLost: bandwidth?.twccReportedLost || 0,
      twccReportedStatuses: bandwidth?.twccReportedStatuses || 0,
      peerConnectionState: peer.connectionState,
      peerConnectionID: peer.__rstreamQualificationID || 0,
      peerConnectionsCreated: peers.length,
      iceConnectionState: peer.iceConnectionState,
      iceGatheringState: peer.iceGatheringState,
      pliCount: inbound?.pliCount || 0,
      playback: text("#playback-status"),
      playoutDelayHintSeconds:
        typeof videoReceiver?.playoutDelayHint === "number"
          ? videoReceiver.playoutDelayHint
          : null,
      producerLocalCandidateProtocol:
        sessionStats?.icePath?.localCandidateProtocol || "",
      producerLocalCandidateType:
        sessionStats?.icePath?.localCandidateType || "",
      producerLocalCandidateURL: sessionStats?.icePath?.localCandidateURL || "",
      producerLocalRelayProtocol:
        sessionStats?.icePath?.localRelayProtocol || "",
      producerRemoteCandidateProtocol:
        sessionStats?.icePath?.remoteCandidateProtocol || "",
      producerRemoteCandidateType:
        sessionStats?.icePath?.remoteCandidateType || "",
      remoteCandidateAddress:
        remoteCandidate?.address || remoteCandidate?.ip || "",
      remoteCandidatePort: remoteCandidate?.port || 0,
      remoteCandidateProtocol: remoteCandidate?.protocol || "",
      remoteCandidateType: remoteCandidate?.candidateType || "",
      iceRestartOffers: telemetry.iceRestartOffers || 0,
      localCandidatesSent: telemetry.localCandidatesSent || 0,
      offersSent: telemetry.offersSent || 0,
      remoteCandidatesReceived: telemetry.remoteCandidatesReceived || 0,
      webSocketCloseCount: telemetry.webSocketCloseCount || 0,
      webSocketID: socket?.__rstreamQualificationID || 0,
      webSocketOpenCount: telemetry.webSocketOpenCount || 0,
      webSocketsCreated: sockets.length,
      webSocketState: socket?.readyState ?? WebSocket.CLOSED,
      recoveryKeyFrameRequests: sessionStats?.recoveryKeyFrameRequests || 0,
      recoveryKeyFrameCoalesced: sessionStats?.recoveryKeyFrameCoalesced || 0,
      recoveryKeyFrameFailures: sessionStats?.recoveryKeyFrameFailures || 0,
      rtcpKeyFrameRequests: sessionStats?.rtcpKeyFrameRequests || 0,
      rtcpMalformedFeedback: sessionStats?.rtcpMalformedFeedback || 0,
      retransmittedBytesReceived: inbound?.retransmittedBytesReceived || 0,
      retransmittedPacketsReceived: inbound?.retransmittedPacketsReceived || 0,
      totalDecodeTimeSeconds: inbound?.totalDecodeTime ?? null,
      fecPacketsDiscarded: inbound?.fecPacketsDiscarded || 0,
      fecPacketsReceived: inbound?.fecPacketsReceived || 0,
      totalFreezesDurationSeconds: inbound?.totalFreezesDuration || 0,
      twccTargetKbps: parseBitrateKbps(text("#twcc-target-status")),
      encoderTargetKbps: parseBitrateKbps(text("#encoder-target-status")),
      videoCurrentTimeSeconds:
        video instanceof HTMLVideoElement ? video.currentTime : 0,
      videoReadyState: video instanceof HTMLVideoElement ? video.readyState : 0,
    };

    function parseBitrateKbps(value) {
      const match = value.match(/^([0-9]+(?:\.[0-9]+)?)\s*(Mbps|kbps)$/i);
      if (!match) {
        return 0;
      }
      const amount = Number.parseFloat(match[1]);
      return match[2].toLowerCase() === "mbps" ? amount * 1000 : amount;
    }
  });
}

async function collectSignalingMetadata(activePage) {
  return activePage.evaluate(() => {
    const telemetry = window.__rstreamQualificationTelemetry || {};
    return {
      events: telemetry.events || [],
      iceRestartOffers: telemetry.iceRestartOffers || 0,
      localCandidatesSent: telemetry.localCandidatesSent || 0,
      offersSent: telemetry.offersSent || 0,
      peerConnectionsCreated:
        window.__rstreamQualificationPeers?.length || 0,
      remoteCandidatesReceived: telemetry.remoteCandidatesReceived || 0,
      webSocketCloseCount: telemetry.webSocketCloseCount || 0,
      webSocketOpenCount: telemetry.webSocketOpenCount || 0,
      webSocketsCreated: window.__rstreamQualificationSockets?.length || 0,
    };
  });
}

async function collectPeerMetadata(activePage) {
  return activePage.evaluate(() => {
    const peers = window.__rstreamQualificationPeers || [];
    const peer = [...peers]
      .reverse()
      .find((candidate) => candidate.connectionState !== "closed");
    if (!peer) {
      throw new Error("the page has no active RTCPeerConnection");
    }
    const codecs = peer
      .getTransceivers()
      .flatMap(
        (transceiver) => transceiver.receiver.getParameters().codecs || [],
      )
      .map((codec) => ({
        channels: codec.channels || null,
        clockRate: codec.clockRate || null,
        mimeType: codec.mimeType || "",
        payloadType: codec.payloadType ?? null,
        sdpFmtpLine: codec.sdpFmtpLine || "",
      }));
    return {
      codecs,
      flexFECNegotiated: codecs.some(
        (codec) => codec.mimeType.toLowerCase() === "video/flexfec-03",
      ),
      rtxNegotiated: codecs.some(
        (codec) => codec.mimeType.toLowerCase() === "video/rtx",
      ),
      transceivers: peer.getTransceivers().map((transceiver) => ({
        currentDirection: transceiver.currentDirection || "",
        direction: transceiver.direction,
        kind: transceiver.receiver.track?.kind || "",
        mid: transceiver.mid || "",
      })),
    };
  });
}

async function navigateWithRetries(activePage, target, attempts) {
  let lastError;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      await activePage.goto(target, {
        waitUntil: "domcontentloaded",
        timeout: 30_000,
      });
      return;
    } catch (error) {
      lastError = normalizeError(error);
      process.stderr.write(
        `browser navigation attempt ${attempt}/${attempts} failed: ${lastError.message}\n`,
      );
      await activePage.goto("about:blank").catch(() => {});
      if (attempt < attempts) {
        await delay(attempt * 2000);
      }
    }
  }
  throw new Error(
    `failed to load ${target} after ${attempts} attempts: ${lastError?.message || "unknown error"}`,
  );
}

async function writeJSONAtomic(path, value) {
  const temporaryPath = `${path}.${process.pid}.tmp`;
  await writeFile(temporaryPath, `${JSON.stringify(value, null, 2)}\n`, {
    encoding: "utf8",
    mode: 0o600,
  });
  await rename(temporaryPath, path);
}

function parseArguments(values) {
  const parsed = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const name = values[index];
    const value = values[index + 1];
    if (!name?.startsWith("--") || value === undefined) {
      throw new Error(`invalid argument near ${name || "end of input"}`);
    }
    parsed.set(name.slice(2), value);
  }
  return parsed;
}

function requiredArgument(values, name) {
  const value = values.get(name);
  if (!value) {
    throw new Error(`--${name} is required`);
  }
  return value;
}

function numberArgument(values, name, fallback) {
  const raw = values.get(name);
  if (raw === undefined) {
    return fallback;
  }
  const value = Number.parseInt(raw, 10);
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`--${name} must be a positive integer`);
  }
  return value;
}

function boundedNumberArgument(values, name, fallback, minimum, maximum) {
  const raw = values.get(name);
  if (raw === undefined) {
    return fallback;
  }
  const value = Number.parseFloat(raw);
  if (!Number.isFinite(value) || value < minimum || value > maximum) {
    throw new Error(`--${name} must be between ${minimum} and ${maximum}`);
  }
  return value;
}

function defaultBrowserExecutable() {
  const candidates = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
  ];
  const executable = candidates.find((candidate) => existsSync(candidate));
  if (!executable) {
    throw new Error(
      "no supported browser found; set BROWSER_EXECUTABLE_PATH explicitly",
    );
  }
  return executable;
}

function normalizeError(error) {
  return error instanceof Error ? error : new Error(String(error));
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
