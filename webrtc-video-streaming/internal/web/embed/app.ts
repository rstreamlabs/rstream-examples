import { z } from "zod";

type TURNPolicy = "auto" | "direct" | "relay";

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
  codec: z.string(),
  twccEnabled: z.boolean(),
  nackEnabled: z.boolean(),
  rtxEnabled: z.boolean(),
  flexFECEnabled: z.boolean(),
  adaptiveBackend: z.enum(["off", "twcc-gcc"]),
  adaptiveActive: z.boolean(),
  estimatedBitrateBps: z.number().int(),
  encoderTargetBitrateKbps: z.number().int(),
  lastAppliedBitrateKbps: z.number().int(),
});

const turnCredentialsSchema = z.object({
  username: z.string(),
  credential: z.string(),
  urls: z.array(z.string()),
});

const signalMessageSchema = z.discriminatedUnion("type", [
  z.object({
    type: z.literal("session.ready"),
    viewerId: z.string().optional(),
  }),
  z.object({
    type: z.literal("webrtc.answer"),
    sdp: z.string(),
  }),
  z.object({
    type: z.literal("webrtc.candidate"),
    candidate: z.string().optional(),
    sdpMid: z.string().nullable().optional(),
    sdpMLineIndex: z.number().int().optional(),
  }),
  z.object({
    type: z.literal("log"),
    message: z.string().optional(),
  }),
  z.object({
    type: z.literal("error"),
    message: z.string().optional(),
  }),
  z.object({
    type: z.literal("session.stats"),
    stats: sessionStatsSchema.optional(),
  }),
]);

const errorResponseSchema = z.object({
  error: z.string().optional(),
});

type SampleInfo = z.infer<typeof sampleInfoSchema>;
type State = {
  info: SampleInfo | null;
  peerConnection: RTCPeerConnection | null;
  webSocket: WebSocket | null;
  viewerId: string | null;
  edgeToken: string | null;
  offerPending: boolean;
  iceRestartTimer: number | null;
};

const EMPTY_LOG_MESSAGE = "No events yet.";

const state: State = {
  info: null,
  peerConnection: null,
  webSocket: null,
  viewerId: null,
  edgeToken: new URL(window.location.href).searchParams.get("rstream.token"),
  offerPending: false,
  iceRestartTimer: null,
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
const wsStatus = requiredHTMLElement("ws-status");
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

function resetLog() {
  logOutput.textContent = EMPTY_LOG_MESSAGE;
}

function log(message: string) {
  if (logOutput.textContent === EMPTY_LOG_MESSAGE) {
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
  if (!capabilities) {
    return false;
  }
  return capabilities.codecs.some(
    (codec) => codec.mimeType.toLowerCase() === mimeType.toLowerCase(),
  );
}

function endpoint(pathname: string, options?: { protocol?: "ws:" | "wss:" }) {
  const url = new URL(pathname, window.location.origin);
  if (state.edgeToken) {
    url.searchParams.set("rstream.token", state.edgeToken);
  }
  if (options?.protocol) {
    url.protocol = options.protocol;
  }
  return url.toString();
}

function setField(node: HTMLElement, value: string) {
  node.textContent = value;
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
  if (value <= 0) {
    return "-";
  }
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)} Mbps`;
  }
  return `${value} kbps`;
}

function setBadge(node: HTMLElement, label: string, value: string) {
  node.textContent = `${label}: ${value}`;
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
  connectButton.disabled = false;
  turnPolicy.disabled = false;
  overlay.classList.remove("hidden");
}

function setConnectedState() {
  disconnectButton.disabled = false;
  connectButton.disabled = true;
  turnPolicy.disabled = true;
}

async function loadInfo() {
  const response = await fetch(endpoint("/api/status"));
  if (!response.ok) {
    throw new Error("Failed to load the sample status");
  }
  state.info = sampleInfoSchema.parse(await response.json());
  const publicURLText = state.info.publicURL ?? "Unavailable";
  publicURL.textContent = publicURLText;
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
  log(`Public URL ready: ${publicURLText}`);
}

async function loadTURNConfiguration(
  policy: TURNPolicy,
): Promise<RTCConfiguration> {
  if (policy === "direct") {
    log("TURN disabled for this viewer session");
    return {};
  }
  const response = await fetch(endpoint("/api/turn"));
  if (!response.ok) {
    const body = errorResponseSchema.parse(
      await response.json().catch(() => ({})),
    );
    throw new Error(body.error || "Failed to load TURN credentials");
  }
  const turn = turnCredentialsSchema.parse(await response.json());
  const configuration: RTCConfiguration = {
    iceServers: [
      {
        urls: turn.urls,
        username: turn.username,
        credential: turn.credential,
      },
    ],
  };
  if (policy === "relay") {
    configuration.iceTransportPolicy = "relay";
    log("TURN relay-only mode enabled");
  } else {
    log("TURN credentials loaded");
  }
  return configuration;
}

async function createPeerConnection(policy: TURNPolicy) {
  const configuration = await loadTURNConfiguration(policy);
  const peerConnection = new RTCPeerConnection(configuration);
  peerConnection.addTransceiver("video", { direction: "recvonly" });
  peerConnection.ontrack = (event) => {
    const [stream] = event.streams;
    if (!stream) {
      return;
    }
    video.srcObject = stream;
    overlay.classList.add("hidden");
    log("Remote video track attached");
    const play = video.play();
    if (play && typeof play.then === "function") {
      play
        .then(() => {
          setField(playbackStatus, "Playing");
          log("Video playback started");
        })
        .catch((error: unknown) => {
          setField(playbackStatus, "Paused");
          if (error instanceof Error) {
            log(error.message);
            return;
          }
          log("Video playback could not start automatically");
        });
      return;
    }
    setField(playbackStatus, "Playing");
  };
  peerConnection.onconnectionstatechange = () => {
    setBadge(peerStatus, "Peer", peerConnection.connectionState);
    log(`Peer connection state: ${peerConnection.connectionState}`);
    if (peerConnection.connectionState === "connected") {
      setConnectedState();
      return;
    }
    if (peerConnection.connectionState === "closed") {
      setDisconnectedState();
      return;
    }
    if (
      peerConnection.connectionState === "disconnected" ||
      peerConnection.connectionState === "failed"
    ) {
      scheduleICERestart(`peer ${peerConnection.connectionState}`);
    }
  };
  peerConnection.oniceconnectionstatechange = () => {
    setBadge(iceStatus, "ICE", peerConnection.iceConnectionState);
    log(`ICE connection state: ${peerConnection.iceConnectionState}`);
    if (
      peerConnection.iceConnectionState === "connected" ||
      peerConnection.iceConnectionState === "completed"
    ) {
      setConnectedState();
      return;
    }
    if (
      peerConnection.iceConnectionState === "disconnected" ||
      peerConnection.iceConnectionState === "failed"
    ) {
      scheduleICERestart(`ICE ${peerConnection.iceConnectionState}`);
    }
  };
  peerConnection.onicecandidate = (event) => {
    if (
      !event.candidate ||
      !state.webSocket ||
      state.webSocket.readyState !== WebSocket.OPEN
    ) {
      return;
    }
    state.webSocket.send(
      JSON.stringify({
        type: "webrtc.candidate",
        candidate: event.candidate.candidate,
        sdpMid: event.candidate.sdpMid,
        sdpMLineIndex: event.candidate.sdpMLineIndex,
      }),
    );
  };
  return peerConnection;
}

function createSignalSocket(policy: TURNPolicy) {
  return new Promise<WebSocket>((resolve, reject) => {
    const socketURL = endpoint("/ws", {
      protocol: window.location.protocol === "https:" ? "wss:" : "ws:",
    });
    const socket = new WebSocket(socketURL);
    socket.onopen = () => {
      setField(wsStatus, "Open");
      log("WebSocket signaling channel opened");
      if (policy === "direct") {
        log("Browser TURN usage is disabled");
      }
      resolve(socket);
    };
    socket.onerror = () => {
      reject(new Error("The signaling socket could not be opened"));
    };
  });
}

async function sendOffer(options: { iceRestart?: boolean } = {}) {
  if (!state.peerConnection || !state.webSocket) {
    throw new Error("The viewer session is not ready");
  }
  if (state.offerPending) {
    return;
  }
  if (state.webSocket.readyState !== WebSocket.OPEN) {
    throw new Error("The signaling socket is not open");
  }
  state.offerPending = true;
  try {
    // An ICE restart keeps the same WebRTC session and gathers fresh
    // candidates after an IP address or network interface change.
    const offer = await state.peerConnection.createOffer(
      options.iceRestart ? { iceRestart: true } : undefined,
    );
    await state.peerConnection.setLocalDescription(offer);
    state.webSocket.send(
      JSON.stringify({ type: "webrtc.offer", sdp: offer.sdp }),
    );
    log(options.iceRestart ? "ICE restart offer sent" : "Offer sent");
  } finally {
    state.offerPending = false;
  }
}

function scheduleICERestart(reason: string) {
  if (
    !state.peerConnection ||
    !state.webSocket ||
    state.iceRestartTimer !== null ||
    state.offerPending ||
    state.peerConnection.signalingState !== "stable" ||
    state.webSocket.readyState !== WebSocket.OPEN
  ) {
    return;
  }
  log(`Scheduling ICE restart after ${reason}`);
  state.iceRestartTimer = window.setTimeout(() => {
    state.iceRestartTimer = null;
    sendOffer({ iceRestart: true }).catch((error: unknown) => {
      if (error instanceof Error) {
        log(error.message);
      } else {
        log("ICE restart failed");
      }
      stop();
    });
  }, 500);
}

async function start() {
  if (state.peerConnection || state.webSocket) {
    return;
  }
  connectButton.disabled = true;
  const policy = currentTURNPolicy();
  try {
    if (state.info && !browserSupportsVideoMimeType(state.info.videoMimeType)) {
      throw new Error(
        `This browser does not advertise WebRTC support for ${state.info.videoMimeType}.`,
      );
    }
    state.peerConnection = await createPeerConnection(policy);
    state.webSocket = await createSignalSocket(policy);
    setConnectedState();
    state.webSocket.onmessage = async (event) => {
      try {
        const message = signalMessageSchema.parse(JSON.parse(event.data));
        switch (message.type) {
          case "session.ready":
            state.viewerId = message.viewerId ?? null;
            setField(viewerID, state.viewerId ?? "Pending");
            log(`Viewer session created: ${state.viewerId ?? "pending"}`);
            break;
          case "webrtc.answer":
            if (!message.sdp) {
              throw new Error(
                "The signaling server returned an invalid answer",
              );
            }
            await state.peerConnection?.setRemoteDescription({
              type: "answer",
              sdp: message.sdp,
            });
            log("Remote answer applied");
            break;
          case "webrtc.candidate":
            if (!message.candidate) {
              return;
            }
            await state.peerConnection?.addIceCandidate({
              candidate: message.candidate,
              sdpMid: message.sdpMid ?? null,
              sdpMLineIndex: message.sdpMLineIndex,
            });
            break;
          case "log":
            if (message.message) {
              log(message.message);
            }
            break;
          case "error":
            throw new Error(message.message || "The server returned an error");
          case "session.stats":
            if (!message.stats) {
              return;
            }
            setField(codecStatus, message.stats.codec.replace("video/", ""));
            setField(
              recoveryStatus,
              [
                message.stats.twccEnabled ? "TWCC" : null,
                message.stats.nackEnabled ? "NACK" : null,
                message.stats.rtxEnabled ? "RTX" : null,
                message.stats.flexFECEnabled ? "FlexFEC" : null,
              ]
                .filter((value) => value !== null)
                .join(", ") || "Off",
            );
            setField(
              adaptiveStatus,
              message.stats.adaptiveActive
                ? message.stats.adaptiveBackend
                : message.stats.adaptiveBackend === "off"
                  ? "Off"
                  : `${message.stats.adaptiveBackend} standby`,
            );
            setField(
              twccTargetStatus,
              formatBitrateBps(message.stats.estimatedBitrateBps),
            );
            setField(
              encoderTargetStatus,
              formatBitrateKbps(message.stats.encoderTargetBitrateKbps),
            );
            break;
        }
      } catch (error: unknown) {
        if (error instanceof Error) {
          log(error.message);
        } else {
          log("The signaling channel returned an invalid response");
        }
        stop();
      }
    };
    state.webSocket.onclose = () => {
      setField(wsStatus, "Closed");
      log("WebSocket signaling channel closed");
      stop();
    };
    await sendOffer();
  } catch (error: unknown) {
    if (error instanceof Error) {
      log(error.message);
    } else {
      log("Failed to start the session");
    }
    stop();
  }
}

function stop() {
  if (state.iceRestartTimer !== null) {
    window.clearTimeout(state.iceRestartTimer);
    state.iceRestartTimer = null;
  }
  state.offerPending = false;
  if (state.webSocket) {
    try {
      state.webSocket.close();
    } catch {}
    state.webSocket = null;
  }
  if (state.peerConnection) {
    try {
      state.peerConnection.close();
    } catch {}
    state.peerConnection = null;
  }
  state.viewerId = null;
  setBadge(peerStatus, "Peer", "idle");
  setBadge(iceStatus, "ICE", "idle");
  setField(wsStatus, "Idle");
  setField(playbackStatus, "Idle");
  setField(
    adaptiveStatus,
    state.info?.adaptiveBackend === "off"
      ? "Off"
      : state.info?.adaptiveBackend || "Off",
  );
  setField(twccTargetStatus, "-");
  setField(encoderTargetStatus, "-");
  setField(viewerID, "Pending");
  video.srcObject = null;
  setDisconnectedState();
}

connectButton.addEventListener("click", () => {
  void start();
});

disconnectButton.addEventListener("click", () => {
  log("Viewer requested shutdown");
  stop();
});

clearLogButton.addEventListener("click", () => {
  resetLog();
});

window.addEventListener("beforeunload", () => {
  stop();
});

loadInfo().catch((error: unknown) => {
  if (error instanceof Error) {
    log(error.message);
    return;
  }
  log("Failed to load the sample");
});

resetLog();
