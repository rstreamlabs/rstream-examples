import {
  appendFile,
  mkdir,
  readFile,
  rename,
  writeFile,
} from "node:fs/promises";
import { existsSync } from "node:fs";
import {
  startQualificationViewer,
  closeServer,
} from "./lib/qualification-viewer.mjs";
import process from "node:process";
import { createHash } from "node:crypto";
import { chromium } from "playwright-core";
import { collectProducerOpenMetrics } from "./lib/openmetrics.mjs";
import {
  collectSourceQuality,
  readSourceQualityCredential,
} from "./lib/quality-sample.mjs";
import { readPhase } from "./lib/phase.mjs";
import { PathStability, pathMatchesPolicy } from "./lib/path.mjs";
import { redactError, redactSensitiveText } from "./lib/redaction.mjs";
import { negotiatedVideoCodecs } from "./lib/sdp-codecs.mjs";
import { installFrameDiagnostics } from "./lib/frame-diagnostics.mjs";
import { installTransitionBoundary } from "./lib/transition-boundary.mjs";
import { installStartupTiming } from "./lib/startup-timing.mjs";
import { createLatencyReport } from "./latency/report.mjs";
import { calibrateBrowserClock, calibrationBounds } from "./latency/clock.mjs";

const argumentsByName = parseArguments(process.argv.slice(2));
const requestedURL = argumentsByName.get("url") || "";
const whepEndpoint = argumentsByName.get("whep-endpoint") || "";
if ((requestedURL === "") === (whepEndpoint === "")) {
  throw new Error("exactly one of --url or --whep-endpoint is required");
}
const outputDirectory = requiredArgument(argumentsByName, "output-directory");
const phaseFile = requiredArgument(argumentsByName, "phase-file");
const producerMetricsURL = argumentsByName.get("producer-metrics-url") || "";
const sourceQualityFile = argumentsByName.get("source-quality-file") || "";
const sourceQualityCredential = sourceQualityFile
  ? await readSourceQualityCredential(sourceQualityFile)
  : null;
const pathScope = whepEndpoint ? "viewer" : "end-to-end";
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
const latencyMode = argumentsByName.get("latency-probe") || "disabled";
if (!["enabled", "disabled"].includes(latencyMode)) {
  throw new Error("latency-probe must be enabled or disabled");
}
const latencyEnabled = latencyMode === "enabled";
let latencyClocks = null;
if (latencyEnabled) {
  if (process.platform !== "linux")
    throw new Error("latency qualification requires a shared Linux host clock");
  const producerBootHash = requiredArgument(
    argumentsByName,
    "producer-boot-hash",
  );
  if (!/^[a-f0-9]{64}$/.test(producerBootHash))
    throw new Error("invalid producer boot hash");
  const receiverBootHash = createHash("sha256")
    .update(await readFile("/proc/sys/kernel/random/boot_id"))
    .digest("hex");
  if (producerBootHash !== receiverBootHash)
    throw new Error("producer and collector must share a Linux host clock");
  const producerMonotonicOffsetHash = requiredArgument(
    argumentsByName,
    "producer-monotonic-offset-hash",
  );
  const receiverMonotonicOffsetHash = createHash("sha256")
    .update(await readFile("/proc/self/timens_offsets"))
    .digest("hex");
  if (
    !/^[a-f0-9]{64}$/.test(producerMonotonicOffsetHash) ||
    producerMonotonicOffsetHash !== receiverMonotonicOffsetHash
  )
    throw new Error(
      "producer and collector must share monotonic clock offsets",
    );
  latencyClocks = {
    producerBootHash,
    receiverBootHash,
    producerMonotonicOffsetHash,
    receiverMonotonicOffsetHash,
  };
}

await mkdir(outputDirectory, { recursive: true });
const samplesPath = `${outputDirectory}/samples.jsonl`;
const readyPath = `${outputDirectory}/collector-ready.json`;
const browserPath = `${outputDirectory}/browser.json`;
const signalingPath = `${outputDirectory}/signaling-events.json`;
const failurePath = `${outputDirectory}/collector-failure.json`;
let browser;
let page;
let viewerServer;
const latencySnapshots = [];
let latencyReportWritten = false;

try {
  let url = requestedURL;
  if (!url) {
    const viewer = await startQualificationViewer(whepEndpoint);
    url = viewer.url;
    viewerServer = viewer.server;
  }
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
    const nativeFetch = window.fetch.bind(window);
    const peers = [];
    const telemetry = {
      events: [],
      iceRestartOffers: 0,
      latestSessionStats: null,
      lastOfferUfrag: "",
      localCandidatesSent: 0,
      offersSent: 0,
      remoteCandidatesReceived: 0,
      whepCandidatePatches: 0,
      whepFailedRequests: 0,
      whepRestartPatches: 0,
      whepSessionCreates: 0,
      whepSessionDeletes: 0,
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
        this.addEventListener("icecandidate", (event) => {
          record(
            event.candidate ? "local-candidate" : "local-candidates-complete",
            {
              peerID,
            },
          );
        });
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
    Object.defineProperty(window, "__rstreamQualificationTelemetry", {
      configurable: false,
      enumerable: false,
      value: telemetry,
      writable: false,
    });
    window.RTCPeerConnection = QualificationRTCPeerConnection;
    window.fetch = async (input, init = {}) => {
      const request = input instanceof Request ? input : null;
      const requestURL = new URL(request?.url ?? String(input), location.href);
      const method = (init.method ?? request?.method ?? "GET").toUpperCase();
      const headers = new Headers(request?.headers);
      for (const [name, value] of new Headers(init.headers)) {
        headers.set(name, value);
      }
      const body = typeof init.body === "string" ? init.body : "";
      const whep = /\/whep(?:\/|$)/.test(requestURL.pathname);
      const diagnostics = requestURL.pathname.startsWith(
        "/api/diagnostics/sessions/",
      );
      const candidateCount = countCandidates(body);
      const patchUfrag = /^a=ice-ufrag:(.+)$/m.exec(body)?.[1] ?? "";
      const restart =
        whep &&
        method === "PATCH" &&
        patchUfrag !== "" &&
        telemetry.lastOfferUfrag !== "" &&
        patchUfrag !== telemetry.lastOfferUfrag;
      if (whep && method === "POST") {
        telemetry.offersSent += 1;
        const ufrag = /^a=ice-ufrag:(.+)$/m.exec(body)?.[1] ?? "";
        if (ufrag) {
          telemetry.lastOfferUfrag = ufrag;
        }
      } else if (whep && method === "PATCH") {
        telemetry.localCandidatesSent += candidateCount;
        if (restart) {
          telemetry.iceRestartOffers += 1;
          telemetry.whepRestartPatches += 1;
          telemetry.lastOfferUfrag = patchUfrag;
        } else {
          telemetry.whepCandidatePatches += 1;
        }
      }
      const startedAt = performance.now();
      try {
        const response = await nativeFetch(input, init);
        if (whep) {
          if (method === "POST" && [201, 406].includes(response.status)) {
            telemetry.whepSessionCreates += 1;
          } else if (
            method === "DELETE" &&
            (response.ok || [404, 410].includes(response.status))
          ) {
            telemetry.whepSessionDeletes += 1;
          }
          record("whep-request", {
            candidateCount,
            durationMilliseconds: Math.round(performance.now() - startedAt),
            method,
            restart,
            status: response.status,
          });
          void response
            .clone()
            .text()
            .then((value) => {
              const remoteCandidates = countCandidates(value);
              telemetry.remoteCandidatesReceived += remoteCandidates;
              if (remoteCandidates > 0) {
                record("remote-candidates", {
                  count: remoteCandidates,
                  method,
                  restart,
                });
              }
            })
            .catch(() => {});
        }
        if (diagnostics && response.ok) {
          void response
            .clone()
            .json()
            .then((value) => {
              telemetry.latestSessionStats = value;
            })
            .catch(() => {});
        }
        return response;
      } catch (error) {
        if (whep) {
          telemetry.whepFailedRequests += 1;
          record("whep-request-failed", {
            durationMilliseconds: Math.round(performance.now() - startedAt),
            method,
            restart,
          });
        }
        throw error;
      }
    };

    function countCandidates(value) {
      return (value.match(/^a=candidate:/gm) || []).length;
    }
  });
  page = await context.newPage();
  page.on("console", (message) => {
    if (message.type() === "error") {
      const location = message.location();
      const source = location.url
        ? ` (${redactSensitiveText(location.url)}:${location.lineNumber || 0}:${location.columnNumber || 0})`
        : "";
      process.stderr.write(
        `browser console error: ${redactSensitiveText(message.text())}${source}\n`,
      );
    }
  });
  page.on("pageerror", (error) => {
    process.stderr.write(
      `browser page error: ${redactSensitiveText(error.message)}\n`,
    );
  });
  page.on("requestfailed", (request) => {
    process.stderr.write(
      `browser request failed: ${request.method()} ${redactSensitiveText(request.url())} (${redactSensitiveText(request.failure()?.errorText || "unknown error")})\n`,
    );
  });
  page.on("response", (response) => {
    if (response.status() >= 400) {
      process.stderr.write(
        `browser response error: ${response.status()} ${response.request().method()} ${redactSensitiveText(response.url())}\n`,
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
  if (!(await page.evaluate(installStartupTiming))) {
    throw new Error("the viewer cannot measure first-frame presentation");
  }
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
  await page.evaluate(() => {
    const telemetry = window.__rstreamQualificationTelemetry;
    telemetry?.events?.push({
      elapsedMilliseconds: Math.round(performance.now()),
      kind: "playback-ready",
    });
  });
  await page.waitForFunction(
    async () => {
      const peers = window.__rstreamQualificationPeers || [];
      const peer = [...peers]
        .reverse()
        .find((candidate) => candidate.connectionState !== "closed");
      if (!peer) {
        return false;
      }
      const reports = await peer.getStats();
      return [...reports.values()].some(
        (report) =>
          report.type === "inbound-rtp" &&
          (report.kind === "video" || report.mediaType === "video") &&
          (report.framesDecoded || 0) > 0,
      );
    },
    undefined,
    { timeout: 30_000 },
  );
  await page.evaluate(() => {
    const telemetry = window.__rstreamQualificationTelemetry;
    telemetry?.events?.push({
      elapsedMilliseconds: Math.round(performance.now()),
      kind: "first-decoded-frame",
    });
  });
  let initialSample = null;
  await page.evaluate(installFrameDiagnostics);
  await page.evaluate(installTransitionBoundary);
  if (latencyEnabled) {
    latencyClocks.initialCalibration = await calibrateBrowserClock(() =>
      page.evaluate(() => ({
        nowMilliseconds: performance.now(),
        timeOriginMilliseconds: performance.timeOrigin,
      })),
    );
    await page.evaluate((offset) => {
      window.__rstreamLatencyClockOffsetMilliseconds = offset;
    }, calibrationBounds(latencyClocks.initialCalibration).offsetMilliseconds);
    await page.evaluate(
      await readFile(new URL("./latency-probe.js", import.meta.url), "utf8"),
    );
  }
  let pathStable = false;
  const pathStability = new PathStability(3000, pathScope);
  const pathDeadline = performance.now() + 30_000;
  while (performance.now() < pathDeadline) {
    initialSample = await collectSample(page);
    if (
      pathMatchesPolicy(initialSample, icePolicy, pathScope) &&
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
    const sample = await collectSample(page, phase);
    if (producerMetricsURL) {
      Object.assign(
        sample,
        await collectProducerOpenMetrics(producerMetricsURL),
      );
    }
    if (sourceQualityCredential) {
      sample.sourceQuality = await collectSourceQuality(
        sourceQualityCredential,
      );
    }
    sample.capturedAt = new Date().toISOString();
    sample.elapsedMilliseconds = Math.round(performance.now() - startedAt);
    sample.phase = phase.name;
    sample.phaseStartedAt = phase.startedAt;
    if (latencyEnabled) {
      if (latencySnapshots.length >= 4096)
        throw new Error("latency snapshot limit exceeded");
      latencySnapshots.push({ phase: sample.phase, latency: sample.latency });
    }
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
  if (latencyEnabled) {
    latencyClocks.finalCalibration = await calibrateBrowserClock(() =>
      page.evaluate(() => ({
        nowMilliseconds: performance.now(),
        timeOriginMilliseconds: performance.timeOrigin,
      })),
    ).catch((error) => {
      latencyClocks.finalCalibrationFailure = redactError(
        normalizeError(error),
      ).message.slice(0, 512);
      return null;
    });
    const report = createLatencyReport(latencySnapshots, latencyClocks, true);
    await writeJSONAtomic(`${outputDirectory}/latency.json`, report);
    latencyReportWritten = true;
    // Invalid measurements remain a failed gate in the runner's final result.
    // Finish collection so teardown, network and resource evidence is retained.
  }
} catch (error) {
  const normalized = redactError(normalizeError(error));
  const pageContext = await collectFailureContext(page);
  await writeJSONAtomic(failurePath, {
    failedAt: new Date().toISOString(),
    message: normalized.message,
    page: pageContext,
    stack: normalized.stack,
  });
  throw normalized;
} finally {
  if (page) {
    await page
      .evaluate(() => {
        window.__rstreamFrameDiagnostics?.stop();
        window.__rstreamTransitionBoundary?.stop();
        window.__rstreamStartupTiming?.stop();
        window.__rstreamLatencyProbe?.stop();
      })
      .catch(() => {});
    await page.click("#disconnect").catch(() => {});
    await page
      .waitForFunction(
        () => {
          const viewer = window.__rstreamQualificationViewer;
          const telemetry = window.__rstreamQualificationTelemetry;
          return (
            (!viewer || viewer.closeResult) &&
            (!telemetry ||
              telemetry.whepSessionCreates === 0 ||
              telemetry.whepSessionDeletes >= telemetry.whepSessionCreates)
          );
        },
        undefined,
        { timeout: 6000 },
      )
      .catch(() => {});
    await collectSignalingMetadata(page)
      .then((metadata) => writeJSONAtomic(signalingPath, metadata))
      .catch(() => {});
  }
  if (browser) {
    await browser.close().catch(() => {});
  }
  if (viewerServer) {
    await closeServer(viewerServer).catch(() => {});
  }
  if (latencyEnabled && !latencyReportWritten) {
    await writeJSONAtomic(
      `${outputDirectory}/latency.json`,
      createLatencyReport(latencySnapshots, latencyClocks, false),
    ).catch(() => {});
  }
}

async function collectFailureContext(activePage) {
  if (!activePage || activePage.isClosed()) {
    return null;
  }
  try {
    const context = await activePage.evaluate(() => {
      const text = (selector) =>
        document.querySelector(selector)?.textContent?.trim() || "";
      return {
        ice: text("#ice-status"),
        log: text("#log").slice(-4096),
        peer: text("#peer-status"),
        playback: text("#playback-status"),
        signaling: text("#signaling-status"),
      };
    });
    return Object.fromEntries(
      Object.entries(context).map(([name, value]) => [
        name,
        redactSensitiveText(value),
      ]),
    );
  } catch {
    return null;
  }
}

async function collectSample(activePage, phase = null) {
  return activePage.evaluate(async (phase) => {
    window.__rstreamTransitionBoundary?.observe(phase);
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
    const inboundRTPReports = inboundReports
      .map((report) => ({
        bytesReceived: report.bytesReceived || 0,
        codecID: report.codecId || "",
        fecPacketsDiscarded: report.fecPacketsDiscarded || 0,
        fecPacketsReceived: report.fecPacketsReceived || 0,
        framesDecoded: report.framesDecoded || 0,
        id: report.id,
        kind: report.kind || report.mediaType || "",
        nackCount: report.nackCount || 0,
        packetsLost: report.packetsLost || 0,
        packetsReceived: report.packetsReceived || 0,
        retransmittedBytesReceived: report.retransmittedBytesReceived || 0,
        retransmittedPacketsReceived: report.retransmittedPacketsReceived || 0,
        ssrc: report.ssrc ?? null,
      }))
      .sort((left, right) => left.id.localeCompare(right.id));
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
    const telemetry = window.__rstreamQualificationTelemetry || {};
    const sessionStats = telemetry.latestSessionStats;
    const bandwidth = sessionStats?.bandwidth;
    const bandwidthNumber = (value) =>
      bandwidth === undefined ? null : (value ?? 0);
    const bandwidthKbps = (value) => {
      const bitrate = bandwidthNumber(value);
      return bitrate === null ? null : bitrate / 1000;
    };
    const sessionNumber = (value) =>
      sessionStats === null || sessionStats === undefined ? null : (value ?? 0);
    const text = (selector) =>
      document.querySelector(selector)?.textContent?.trim() || "";
    return {
      bytesReceived: inbound?.bytesReceived || 0,
      codecID: inbound?.codecId || "",
      currentRoundTripTimeSeconds: selectedPair?.currentRoundTripTime ?? null,
      decoderImplementation: inbound?.decoderImplementation || "",
      delayControllerState:
        bandwidth === undefined ? null : (bandwidth.state ?? ""),
      delayControllerUsage:
        bandwidth === undefined ? null : (bandwidth.usage ?? ""),
      delayEstimateMilliseconds: bandwidth?.delayEstimateMs ?? null,
      delayMeasurementMilliseconds: bandwidth?.delayMeasurementMs ?? null,
      delayTargetKbps: bandwidthKbps(bandwidth?.delayTargetBitrateBps),
      delayThresholdMilliseconds: bandwidth?.delayThresholdMs ?? null,
      estimatedPlayoutTimestamp: inbound?.estimatedPlayoutTimestamp ?? null,
      framesDecoded: inbound?.framesDecoded || 0,
      framePresentation: window.__rstreamFrameDiagnostics?.drain() ?? null,
      transitionBoundary: window.__rstreamTransitionBoundary?.snapshot() ?? null,
      latency: window.__rstreamLatencyProbe?.read() ?? null,
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
      lossGuardActive:
        bandwidth === undefined ? null : Boolean(bandwidth.lossGuardActive),
      lossGuardTargetKbps: bandwidthKbps(bandwidth?.lossGuardTargetBitrateBps),
      lossGuardLastObservedLoss: bandwidth?.lossGuardLastObservedLoss ?? null,
      lossGuardReductions: bandwidthNumber(bandwidth?.lossGuardReductions),
      lossGuardRecoveries: bandwidthNumber(bandwidth?.lossGuardRecoveries),
      flexFECMediaPackets: bandwidthNumber(bandwidth?.flexFECMediaPackets),
      flexFECRepairPackets: bandwidthNumber(bandwidth?.flexFECRepairPackets),
      lossTargetKbps: bandwidthKbps(bandwidth?.lossTargetBitrateBps),
      nackCount: inbound?.nackCount || 0,
      packetsDiscarded: inbound?.packetsDiscarded || 0,
      packetsLost: inbound?.packetsLost || 0,
      packetsReceived: inbound?.packetsReceived || 0,
      pacerTargetBitrateKbps: bandwidthKbps(bandwidth?.pacerTargetBitrateBps),
      pacerPacingBitrateKbps: bandwidthKbps(bandwidth?.pacerPacingBitrateBps),
      pacerQueueDrops: bandwidthNumber(bandwidth?.pacerQueueDrops),
      pacerQueueDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerQueueDelayMs,
      ),
      pacerMaximumQueueDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumDelayMs,
      ),
      pacerMaximumPrimaryDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumPrimaryDelayMs,
      ),
      pacerMaximumRepairDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumRepairDelayMs,
      ),
      pacerMaximumRetransmissionDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumRetransmissionDelayMs,
      ),
      pacerMaximumFECDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumFECDelayMs,
      ),
      pacerMaximumSustainedDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumSustainedDelayMs,
      ),
      pacerMaximumAdmittedDelayMilliseconds: bandwidthNumber(
        bandwidth?.pacerMaximumAdmittedDelayMs,
      ),
      pacerKeyFrameReserveBytes: bandwidthNumber(
        bandwidth?.pacerKeyFrameReserveBytes,
      ),
      pacerMediaFramesDropped: bandwidthNumber(bandwidth?.pacerMediaFrameDrops),
      pacerMediaBytesDropped: bandwidthNumber(bandwidth?.pacerMediaByteDrops),
      pacerRepairPacketsExpired: bandwidthNumber(
        bandwidth?.pacerRepairPacketsExpired,
      ),
      pacerRepairPacketsTrimmed: bandwidthNumber(
        bandwidth?.pacerRepairPacketsTrimmed,
      ),
      pacerRetransmissionPacketsExpired: bandwidthNumber(
        bandwidth?.pacerRetransmissionPacketsExpired,
      ),
      pacerRetransmissionPacketsCoalesced: bandwidthNumber(
        bandwidth?.pacerRetransmissionPacketsCoalesced,
      ),
      pacerRetransmissionPacketsSuppressed: bandwidthNumber(
        bandwidth?.pacerRetransmissionPacketsSuppressed,
      ),
      pacerRetransmissionRTTMilliseconds: bandwidthNumber(
        bandwidth?.pacerRetransmissionRTTMs,
      ),
      pacerRetransmissionIntervalMilliseconds: bandwidthNumber(
        bandwidth?.pacerRetransmissionIntervalMs,
      ),
      pacerFECPacketsExpired: bandwidthNumber(
        bandwidth?.pacerFECPacketsExpired,
      ),
      pacerRetransmissionPacketsTrimmed: bandwidthNumber(
        bandwidth?.pacerRetransmissionPacketsTrimmed,
      ),
      pacerFECPacketsTrimmed: bandwidthNumber(
        bandwidth?.pacerFECPacketsTrimmed,
      ),
      pacerQueuePackets: bandwidthNumber(bandwidth?.pacerQueuePackets),
      pacerSentPrimary: bandwidthNumber(bandwidth?.pacerSentPrimary),
      pacerSentRepair: bandwidthNumber(bandwidth?.pacerSentRepair),
      pacerSentRetransmission: bandwidthNumber(
        bandwidth?.pacerSentRetransmission,
      ),
      pacerSentFEC: bandwidthNumber(bandwidth?.pacerSentFEC),
      pacerPrimarySSRC: bandwidthNumber(bandwidth?.pacerPrimarySSRC),
      pacerRetransmissionSSRC: bandwidthNumber(
        bandwidth?.pacerRetransmissionSSRC,
      ),
      pacerForwardErrorCorrectionSSRC: bandwidthNumber(
        bandwidth?.pacerForwardErrorCorrectionSSRC,
      ),
      pacerFirstRetransmissionSequence:
        bandwidth?.pacerFirstRetransmissionSequence ?? null,
      pacerLastRetransmissionSequence:
        bandwidth?.pacerLastRetransmissionSequence ?? null,
      pacerRetransmissionSequenceSamples: bandwidthNumber(
        bandwidth?.pacerRetransmissionSequenceSamples,
      ),
      adaptiveBitrateUpdates: sessionNumber(
        sessionStats?.adaptiveBitrateUpdates,
      ),
      adaptiveBitrateFailures: sessionNumber(
        sessionStats?.adaptiveBitrateFailures,
      ),
      staleBitrateCallbacks: bandwidthNumber(bandwidth?.staleBitrateCallbacks),
      twccFeedbackPackets: bandwidthNumber(bandwidth?.twccFeedbackPackets),
      twccMalformedFeedback: bandwidthNumber(bandwidth?.twccMalformedFeedback),
      twccPaddingStatuses: bandwidthNumber(bandwidth?.twccPaddingStatuses),
      twccReportedLost: bandwidthNumber(bandwidth?.twccReportedLost),
      twccReportedStatuses: bandwidthNumber(bandwidth?.twccReportedStatuses),
      peerConnectionState: peer.connectionState,
      peerConnectionID: peer.__rstreamQualificationID || 0,
      peerConnectionsCreated: peers.length,
      iceConnectionState: peer.iceConnectionState,
      iceGatheringState: peer.iceGatheringState,
      inboundRTPReports,
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
      whepCandidatePatches: telemetry.whepCandidatePatches || 0,
      whepFailedRequests: telemetry.whepFailedRequests || 0,
      whepRestartPatches: telemetry.whepRestartPatches || 0,
      whepSessionCreates: telemetry.whepSessionCreates || 0,
      whepSessionDeletes: telemetry.whepSessionDeletes || 0,
      recoveryKeyFrameRequests: sessionNumber(
        sessionStats?.recoveryKeyFrameRequests,
      ),
      recoveryKeyFrameCoalesced: sessionNumber(
        sessionStats?.recoveryKeyFrameCoalesced,
      ),
      recoveryKeyFrameFailures: sessionNumber(
        sessionStats?.recoveryKeyFrameFailures,
      ),
      rtcpKeyFrameRequests: sessionNumber(sessionStats?.rtcpKeyFrameRequests),
      rtcpMalformedFeedback: sessionNumber(sessionStats?.rtcpMalformedFeedback),
      retransmittedBytesReceived: inbound?.retransmittedBytesReceived || 0,
      retransmittedPacketsReceived: inbound?.retransmittedPacketsReceived || 0,
      qpSum: inbound?.qpSum ?? null,
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
        return null;
      }
      const amount = Number.parseFloat(match[1]);
      return match[2].toLowerCase() === "mbps" ? amount * 1000 : amount;
    }
  }, phase);
}

async function collectSignalingMetadata(activePage) {
  return activePage.evaluate(() => {
    const telemetry = window.__rstreamQualificationTelemetry || {};
    return {
      closeResult: window.__rstreamQualificationViewer?.closeResult || null,
      startup: window.__rstreamStartupTiming?.snapshot() ?? null,
      events: telemetry.events || [],
      iceRestartOffers: telemetry.iceRestartOffers || 0,
      localCandidatesSent: telemetry.localCandidatesSent || 0,
      offersSent: telemetry.offersSent || 0,
      peerConnectionsCreated: window.__rstreamQualificationPeers?.length || 0,
      remoteCandidatesReceived: telemetry.remoteCandidatesReceived || 0,
      whepCandidatePatches: telemetry.whepCandidatePatches || 0,
      whepFailedRequests: telemetry.whepFailedRequests || 0,
      whepRestartPatches: telemetry.whepRestartPatches || 0,
      whepSessionCreates: telemetry.whepSessionCreates || 0,
      whepSessionDeletes: telemetry.whepSessionDeletes || 0,
    };
  });
}

async function collectPeerMetadata(activePage) {
  const metadata = await activePage.evaluate(() => {
    const peers = window.__rstreamQualificationPeers || [];
    const peer = [...peers]
      .reverse()
      .find((candidate) => candidate.connectionState !== "closed");
    if (!peer) {
      throw new Error("the page has no active RTCPeerConnection");
    }
    return {
      remoteDescription: peer.remoteDescription?.sdp || "",
      transceivers: peer.getTransceivers().map((transceiver) => ({
        currentDirection: transceiver.currentDirection || "",
        direction: transceiver.direction,
        kind: transceiver.receiver.track?.kind || "",
        mid: transceiver.mid || "",
      })),
    };
  });
  const codecs = negotiatedVideoCodecs(metadata.remoteDescription);
  return {
    codecs,
    flexFECNegotiated: codecs.some(
      (codec) => codec.mimeType.toLowerCase() === "video/flexfec-03",
    ),
    nackNegotiated: codecs.some((codec) =>
      codec.rtcpFeedback.some(
        (value) => value === "nack" || value.startsWith("nack "),
      ),
    ),
    rtxNegotiated: codecs.some(
      (codec) => codec.mimeType.toLowerCase() === "video/rtx",
    ),
    twccNegotiated: codecs.some((codec) =>
      codec.rtcpFeedback.includes("transport-cc"),
    ),
    transceivers: metadata.transceivers,
  };
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
        `browser navigation attempt ${attempt}/${attempts} failed: ${redactSensitiveText(lastError.message)}\n`,
      );
      await activePage.goto("about:blank").catch(() => {});
      if (attempt < attempts) {
        await delay(attempt * 2000);
      }
    }
  }
  throw new Error(
    `failed to load ${redactSensitiveText(target)} after ${attempts} attempts: ${redactSensitiveText(lastError?.message || "unknown error")}`,
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
