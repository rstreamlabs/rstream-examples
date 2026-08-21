import { z } from "zod";

import { WHEPClient } from "../../../../shared/whep-client";
import { expiringTurnCredentialsSchema } from "./contracts";
import { ICERestartTimers, iceRestartDisposition } from "./ice-recovery";

type TURNPolicy = "auto" | "direct" | "relay";

const diagnosticsPollIntervalMs = 1_000;
const diagnosticsRequestTimeoutMs = 3_000;
const iceRestartDelayMs = 750;
const iceRestartOutcomeTimeoutMs = 15_000;
const maxICERestarts = 2;

const sampleInfoSchema = z.object({
  adaptiveBackend: z.enum(["off", "twcc-gcc"]),
  flexFECEnabled: z.boolean(),
  localURL: z.string(),
  nackEnabled: z.boolean(),
  publicURL: z.string().nullable().optional(),
  rtxEnabled: z.boolean(),
  tunnelAuth: z.object({
    token: z.boolean(),
    rstream: z.boolean(),
  }),
  twccEnabled: z.boolean(),
  videoMimeType: z.string(),
});

const sessionStatsSchema = z.object({
  adaptiveActive: z.boolean(),
  adaptiveBackend: z.enum(["off", "twcc-gcc"]),
  codec: z.string(),
  encoderTargetBitrateKbps: z.number().int(),
  estimatedBitrateBps: z.number().int(),
  flexFECEnabled: z.boolean(),
  flexFECNegotiated: z.boolean(),
  lastAppliedBitrateKbps: z.number().int(),
  nackEnabled: z.boolean(),
  nackNegotiated: z.boolean(),
  rtxEnabled: z.boolean(),
  rtxNegotiated: z.boolean(),
  twccEnabled: z.boolean(),
  twccNegotiated: z.boolean(),
});

const errorResponseSchema = z.object({
  error: z.string().optional(),
});

type SampleInfo = z.infer<typeof sampleInfoSchema>;
type TURNConfiguration = {
  configuration: RTCConfiguration;
  expiresAt: string | null;
};
type State = {
  client: WHEPClient | null;
  diagnosticsAbort: AbortController | null;
  diagnosticsTimer: number | null;
  edgeToken: string | null;
  iceRestartAttempts: number;
  iceRestartInFlight: boolean;
  info: SampleInfo | null;
  sessionID: number;
};

const emptyLogMessage = "No events yet.";
const diagnosticsHeader = "X-Rstream-Diagnostics-Token";

const state: State = {
  client: null,
  diagnosticsAbort: null,
  diagnosticsTimer: null,
  edgeToken: new URL(window.location.href).searchParams.get("rstream.token"),
  iceRestartAttempts: 0,
  iceRestartInFlight: false,
  info: null,
  sessionID: 0,
};

function requiredHTMLElement(id: string): HTMLElement {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLElement)) {
    throw new Error(`Missing element #${id}`);
  }
  return element;
}

function requiredButtonElement(id: string): HTMLButtonElement {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLButtonElement)) {
    throw new Error(`Missing button #${id}`);
  }
  return element;
}

function requiredVideoElement(id: string): HTMLVideoElement {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLVideoElement)) {
    throw new Error(`Missing video #${id}`);
  }
  return element;
}

function requiredSelectElement(id: string): HTMLSelectElement {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLSelectElement)) {
    throw new Error(`Missing select #${id}`);
  }
  return element;
}

const publicURL = requiredHTMLElement("public-url");
const tunnelAuth = requiredHTMLElement("tunnel-auth");
const peerStatus = requiredHTMLElement("peer-status");
const iceStatus = requiredHTMLElement("ice-status");
const signalingStatus = requiredHTMLElement("signaling-status");
const playbackStatus = requiredHTMLElement("playback-status");
const codecStatus = requiredHTMLElement("codec-status");
const recoveryStatus = requiredHTMLElement("recovery-status");
const adaptiveStatus = requiredHTMLElement("adaptive-status");
const twccTargetStatus = requiredHTMLElement("twcc-target-status");
const encoderTargetStatus = requiredHTMLElement("encoder-target-status");
const viewerID = requiredHTMLElement("viewer-id");
const overlay = requiredHTMLElement("video-overlay");
const video = requiredVideoElement("video");
const logOutput = requiredHTMLElement("log");
const connectButton = requiredButtonElement("connect");
const disconnectButton = requiredButtonElement("disconnect");
const clearLogButton = requiredButtonElement("clear-log");
const turnPolicy = requiredSelectElement("turn-policy");
const iceRestartTimers = new ICERestartTimers();

function resetLog() {
  logOutput.textContent = emptyLogMessage;
}

function log(message: string) {
  if (logOutput.textContent === emptyLogMessage) {
    logOutput.textContent = "";
  }
  const timestamp = new Date().toLocaleTimeString();
  logOutput.textContent += `[${timestamp}] ${message}\n`;
  logOutput.scrollTop = logOutput.scrollHeight;
}

function currentTURNPolicy(): TURNPolicy {
  if (turnPolicy.value === "direct") {
    return "direct";
  }
  if (turnPolicy.value === "relay") {
    return "relay";
  }
  return "auto";
}

function browserSupportsVideoMimeType(mimeType: string): boolean {
  const capabilities = RTCRtpReceiver.getCapabilities("video");
  return (
    capabilities?.codecs.some(
      (codec) => codec.mimeType.toLowerCase() === mimeType.toLowerCase(),
    ) ?? false
  );
}

function endpoint(pathname: string) {
  const url = new URL(pathname, window.location.origin);
  if (state.edgeToken) {
    url.searchParams.set("rstream.token", state.edgeToken);
  }
  return url.toString();
}

function setField(node: HTMLElement, value: string) {
  node.textContent = value;
}

function setBadge(node: HTMLElement, label: string, value: string) {
  node.textContent = `${label}: ${value}`;
}

function formatBitrateBps(value: number): string {
  if (value <= 0) {
    return "-";
  }
  if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(1)} Mbps`;
  }
  return `${Math.round(value / 1_000)} kbps`;
}

function formatBitrateKbps(value: number): string {
  return formatBitrateBps(value * 1_000);
}

function formatTunnelAuth(auth: SampleInfo["tunnelAuth"]): string {
  const modes = [
    auth.token ? "Token" : null,
    auth.rstream ? "rstream" : null,
  ].filter((value) => value !== null);
  return modes.join(" + ") || "Off";
}

function setDisconnectedState() {
  disconnectButton.disabled = true;
  connectButton.disabled = state.info === null;
  turnPolicy.disabled = state.info === null;
  overlay.classList.remove("hidden");
}

function setConnectedState() {
  disconnectButton.disabled = false;
  connectButton.disabled = true;
  turnPolicy.disabled = true;
}

function isCurrentSession(sessionID: number) {
  return state.sessionID === sessionID;
}

async function loadInfo() {
  const response = await fetch(endpoint("/api/status"), {
    cache: "no-store",
    credentials: "omit",
  });
  if (!response.ok) {
    throw new Error("Failed to load the sample status");
  }
  state.info = sampleInfoSchema.parse(await response.json());
  const publicURLText = state.info.publicURL ?? "Unavailable";
  setField(publicURL, publicURLText);
  setBadge(tunnelAuth, "Auth", formatTunnelAuth(state.info.tunnelAuth));
  setField(codecStatus, state.info.videoMimeType.replace("video/", ""));
  setField(
    recoveryStatus,
    [
      state.info.twccEnabled ? "TWCC" : null,
      state.info.nackEnabled ? "NACK" : null,
      state.info.rtxEnabled ? "RTX" : null,
      state.info.flexFECEnabled ? "FlexFEC" : null,
    ]
      .filter((value) => value !== null)
      .join(", ") || "Off",
  );
  setField(
    adaptiveStatus,
    state.info.adaptiveBackend === "off" ? "Off" : state.info.adaptiveBackend,
  );
  log(`Public URL: ${publicURLText}`);
}

async function loadTURNConfiguration(
  policy: TURNPolicy,
  signal?: AbortSignal,
): Promise<TURNConfiguration> {
  if (policy === "direct") {
    log("TURN disabled for this viewer session");
    return { configuration: {}, expiresAt: null };
  }
  const response = await fetch(endpoint("/api/turn"), {
    cache: "no-store",
    credentials: "omit",
    signal,
  });
  if (!response.ok) {
    const body = errorResponseSchema.parse(
      await response.json().catch(() => ({})),
    );
    throw new Error(body.error || "Failed to load TURN credentials");
  }
  const turn = expiringTurnCredentialsSchema.parse(await response.json());
  const configuration: RTCConfiguration = {
    iceServers: [
      {
        credential: turn.credential,
        urls: turn.urls,
        username: turn.username,
      },
    ],
  };
  if (policy === "relay") {
    configuration.iceTransportPolicy = "relay";
    log("TURN relay-only mode enabled");
  } else {
    log("TURN credentials loaded");
  }
  return { configuration, expiresAt: turn.expiresAt };
}

function handleTrack(event: RTCTrackEvent, sessionID: number) {
  if (!isCurrentSession(sessionID)) {
    return;
  }
  video.srcObject = event.streams[0] ?? new MediaStream([event.track]);
  overlay.classList.add("hidden");
  log("Remote video track attached");
  void video
    .play()
    .then(() => {
      if (isCurrentSession(sessionID)) {
        setField(playbackStatus, "Playing");
        log("Video playback started");
      }
    })
    .catch((error: unknown) => {
      if (!isCurrentSession(sessionID)) {
        return;
      }
      setField(playbackStatus, "Paused");
      log(error instanceof Error ? error.message : "Playback did not start");
    });
}

function configurePeer(client: WHEPClient, sessionID: number) {
  client.peer.onconnectionstatechange = () => {
    if (!isCurrentSession(sessionID)) {
      return;
    }
    const connectionState = client.peer.connectionState;
    setBadge(peerStatus, "Peer", connectionState);
    log(`Peer connection state: ${connectionState}`);
    if (connectionState === "connected") {
      iceRestartTimers.clear();
      state.iceRestartAttempts = 0;
      setConnectedState();
    } else if (connectionState === "failed") {
      if (!state.iceRestartInFlight) {
        iceRestartTimers.clearOutcome();
      }
      scheduleICERestart(sessionID, "peer connection failed");
    }
  };
  client.peer.oniceconnectionstatechange = () => {
    if (!isCurrentSession(sessionID)) {
      return;
    }
    const iceState = client.peer.iceConnectionState;
    setBadge(iceStatus, "ICE", iceState);
    log(`ICE connection state: ${iceState}`);
    if (iceState === "connected" || iceState === "completed") {
      iceRestartTimers.clear();
      state.iceRestartAttempts = 0;
      setConnectedState();
    } else if (iceState === "disconnected" || iceState === "failed") {
      if (!state.iceRestartInFlight) {
        iceRestartTimers.clearOutcome();
      }
      scheduleICERestart(sessionID, `ICE ${iceState}`);
    }
  };
}

function scheduleICERestart(sessionID: number, reason: string) {
  if (
    !isCurrentSession(sessionID) ||
    !state.client ||
    state.iceRestartInFlight ||
    iceRestartTimers.retryScheduled
  ) {
    return;
  }
  if (state.iceRestartAttempts >= maxICERestarts) {
    log(`WHEP recovery stopped after ${maxICERestarts} ICE restarts`);
    stop(sessionID);
    return;
  }
  log(`Scheduling an ICE restart after ${reason}`);
  iceRestartTimers.scheduleRetry(() => {
    const client = state.client;
    if (!client || !isCurrentSession(sessionID)) {
      return;
    }
    state.iceRestartAttempts += 1;
    state.iceRestartInFlight = true;
    iceRestartTimers.scheduleOutcome(() => {
      if (!isCurrentSession(sessionID) || state.client !== client) {
        return;
      }
      state.iceRestartInFlight = false;
      scheduleICERestart(sessionID, "restart did not restore connectivity");
    }, iceRestartOutcomeTimeoutMs);
    let retry = false;
    void client
      .restart()
      .then(() => {
        if (isCurrentSession(sessionID)) {
          log(`ICE restart ${state.iceRestartAttempts} completed`);
          const connectionState = client.peer.connectionState;
          const iceState = client.peer.iceConnectionState;
          const disposition = iceRestartDisposition(connectionState, iceState);
          if (disposition === "connected") {
            iceRestartTimers.clearOutcome();
          } else if (disposition === "retry") {
            iceRestartTimers.clearOutcome();
            retry = true;
          }
        }
      })
      .catch((error: unknown) => {
        if (!isCurrentSession(sessionID)) {
          return;
        }
        iceRestartTimers.clearOutcome();
        log(error instanceof Error ? error.message : "ICE restart failed");
        retry = true;
      })
      .finally(() => {
        if (!isCurrentSession(sessionID)) {
          return;
        }
        state.iceRestartInFlight = false;
        if (retry) {
          scheduleICERestart(sessionID, "restart failure");
        }
      });
  }, iceRestartDelayMs);
}

function sessionIdentifier(client: WHEPClient) {
  const resource = client.sessionResource();
  if (!resource) {
    throw new Error("WHEP endpoint did not return a session resource");
  }
  const identifier = new URL(resource).pathname
    .split("/")
    .filter(Boolean)
    .at(-1);
  if (!identifier) {
    throw new Error("WHEP endpoint returned an invalid session resource");
  }
  return identifier;
}

function startDiagnostics(client: WHEPClient, sessionID: number) {
  const token = client.sessionHeader(diagnosticsHeader);
  if (!token) {
    log("Session diagnostics are unavailable");
    return;
  }
  const identifier = sessionIdentifier(client);
  setField(viewerID, identifier);
  const poll = async () => {
    if (!isCurrentSession(sessionID)) {
      return;
    }
    const abort = new AbortController();
    state.diagnosticsAbort = abort;
    const timeout = window.setTimeout(
      () => abort.abort(),
      diagnosticsRequestTimeoutMs,
    );
    try {
      const response = await fetch(
        endpoint(`/api/diagnostics/sessions/${encodeURIComponent(identifier)}`),
        {
          cache: "no-store",
          credentials: "omit",
          headers: { Authorization: `Bearer ${token}` },
          signal: abort.signal,
        },
      );
      if (!response.ok) {
        throw new Error(`Session diagnostics failed (${response.status})`);
      }
      applySessionStats(sessionStatsSchema.parse(await response.json()));
    } catch (error: unknown) {
      if (!abort.signal.aborted && isCurrentSession(sessionID)) {
        log(
          error instanceof Error ? error.message : "Session diagnostics failed",
        );
      }
    } finally {
      window.clearTimeout(timeout);
      if (state.diagnosticsAbort === abort) {
        state.diagnosticsAbort = null;
      }
      if (isCurrentSession(sessionID)) {
        state.diagnosticsTimer = window.setTimeout(
          () => void poll(),
          diagnosticsPollIntervalMs,
        );
      }
    }
  };
  void poll();
}

function applySessionStats(stats: z.infer<typeof sessionStatsSchema>) {
  setField(codecStatus, stats.codec.replace("video/", ""));
  setField(recoveryStatus, formatNegotiatedTransport(stats));
  setField(
    adaptiveStatus,
    stats.adaptiveActive
      ? stats.adaptiveBackend
      : stats.adaptiveBackend === "off"
        ? "Off"
        : `${stats.adaptiveBackend} standby`,
  );
  setField(twccTargetStatus, formatBitrateBps(stats.estimatedBitrateBps));
  setField(
    encoderTargetStatus,
    formatBitrateKbps(stats.encoderTargetBitrateKbps),
  );
}

function formatNegotiatedTransport(stats: z.infer<typeof sessionStatsSchema>) {
  return (
    [
      formatTransportFeature("TWCC", stats.twccEnabled, stats.twccNegotiated),
      formatTransportFeature("NACK", stats.nackEnabled, stats.nackNegotiated),
      formatTransportFeature("RTX", stats.rtxEnabled, stats.rtxNegotiated),
      formatTransportFeature(
        "FlexFEC",
        stats.flexFECEnabled,
        stats.flexFECNegotiated,
      ),
    ]
      .filter((value) => value !== null)
      .join(", ") || "Off"
  );
}

function formatTransportFeature(
  name: string,
  enabled: boolean,
  negotiated: boolean,
) {
  if (!enabled) {
    return null;
  }
  return negotiated ? name : `${name} unavailable`;
}

async function start() {
  if (state.client || !state.info) {
    return;
  }
  const sessionID = state.sessionID + 1;
  state.sessionID = sessionID;
  connectButton.disabled = true;
  state.iceRestartAttempts = 0;
  state.iceRestartInFlight = false;
  try {
    if (!browserSupportsVideoMimeType(state.info.videoMimeType)) {
      throw new Error(
        `This browser does not advertise WebRTC support for ${state.info.videoMimeType}.`,
      );
    }
    const policy = currentTURNPolicy();
    const turn = await loadTURNConfiguration(policy);
    if (!isCurrentSession(sessionID)) {
      return;
    }
    const client = new WHEPClient({
      allowInsecureHTTP:
        state.info.publicURL == null && window.location.protocol === "http:",
      authorization: "",
      credentialExpiresAt: turn.expiresAt ?? undefined,
      endpoint: endpoint("/whep"),
      iceCredentialExpiresAt: turn.expiresAt ?? undefined,
      iceServers: turn.configuration.iceServers ?? [],
      onError: (error) => {
        if (isCurrentSession(sessionID)) {
          log(error.message);
          log("The WHEP signaling session failed and must be reconnected");
          stop(sessionID);
        }
      },
      onTrack: (event) => handleTrack(event, sessionID),
      peerFactory: (peerConfiguration) =>
        new RTCPeerConnection({
          ...peerConfiguration,
          ...turn.configuration,
        }),
      refreshCredentials:
        policy === "direct"
          ? undefined
          : async (signal) => {
              const refreshed = await loadTURNConfiguration(policy, signal);
              if (!refreshed.expiresAt) {
                throw new Error("TURN credentials omitted their expiration");
              }
              return {
                authorization: "",
                endpoint: endpoint("/whep"),
                expiresAt: refreshed.expiresAt,
                iceExpiresAt: refreshed.expiresAt,
                iceServers: refreshed.configuration.iceServers ?? [],
              };
            },
    });
    state.client = client;
    configurePeer(client, sessionID);
    setField(signalingStatus, "Negotiating");
    setConnectedState();
    await client.start();
    if (!isCurrentSession(sessionID)) {
      await client.close();
      return;
    }
    setField(signalingStatus, "WHEP active");
    log("WHEP session established");
    startDiagnostics(client, sessionID);
  } catch (error: unknown) {
    if (!isCurrentSession(sessionID)) {
      return;
    }
    log(error instanceof Error ? error.message : "Failed to start the session");
    stop(sessionID);
  }
}

function stop(expectedSessionID?: number) {
  if (expectedSessionID !== undefined && !isCurrentSession(expectedSessionID)) {
    return;
  }
  state.sessionID += 1;
  state.iceRestartInFlight = false;
  iceRestartTimers.clear();
  if (state.diagnosticsTimer !== null) {
    window.clearTimeout(state.diagnosticsTimer);
    state.diagnosticsTimer = null;
  }
  state.diagnosticsAbort?.abort();
  state.diagnosticsAbort = null;
  const client = state.client;
  state.client = null;
  if (client) {
    void client.close();
  }
  const stream = video.srcObject;
  if (stream instanceof MediaStream) {
    for (const track of stream.getTracks()) {
      track.stop();
    }
  }
  video.srcObject = null;
  setBadge(peerStatus, "Peer", "idle");
  setBadge(iceStatus, "ICE", "idle");
  setField(signalingStatus, "Idle");
  setField(playbackStatus, "Idle");
  setField(viewerID, "Pending");
  setField(twccTargetStatus, "-");
  setField(encoderTargetStatus, "-");
  setDisconnectedState();
}

connectButton.addEventListener("click", () => {
  void start();
});

disconnectButton.addEventListener("click", () => {
  log("Viewer requested shutdown");
  stop();
});

clearLogButton.addEventListener("click", resetLog);

window.addEventListener("beforeunload", () => {
  stop();
});

resetLog();

void loadInfo()
  .then(setDisconnectedState)
  .catch((error: unknown) => {
    log(error instanceof Error ? error.message : "Failed to load the sample");
  });
