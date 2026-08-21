import { WHEPClient, type WHEPCloseResult } from "../../../shared/whep-client";

declare global {
  interface Window {
    __rstreamQualificationViewer: {
      authorization: string;
      closeResult?: WHEPCloseResult | null;
      endpoint: string;
    };
  }
}

const config = window.__rstreamQualificationViewer;
config.closeResult = null;
const video = requiredVideo("video");
const connect = requiredButton("connect");
const disconnect = requiredButton("disconnect");
const peerStatus = requiredElement("peer-status");
const playbackStatus = requiredElement("playback-status");
const signalingStatus = requiredElement("signaling-status");
let client: WHEPClient | null = null;

connect.addEventListener("click", () => void start());
disconnect.addEventListener("click", () => void stop());
connect.disabled = false;

async function start() {
  if (client) {
    return;
  }
  connect.disabled = true;
  signalingStatus.textContent = "Connecting";
  const current = new WHEPClient({
    allowInsecureHTTP: true,
    allowLegacyWildcardETag: true,
    authorization: config.authorization,
    endpoint: config.endpoint,
    iceServers: [],
    onClose: (result) => {
      config.closeResult = result;
    },
    onError: (error) => {
      signalingStatus.textContent = error.message;
    },
    onTrack: (event) => {
      video.srcObject = event.streams[0] ?? new MediaStream([event.track]);
      void video.play().then(() => {
        playbackStatus.textContent = "Playing";
      });
    },
  });
  client = current;
  current.peer.addEventListener("connectionstatechange", () => {
    peerStatus.textContent = `Peer: ${current.peer.connectionState}`;
  });
  try {
    await current.start();
    signalingStatus.textContent = "Connected";
    disconnect.disabled = false;
  } catch (error) {
    await stop();
    signalingStatus.textContent =
      error instanceof Error ? error.message : "WHEP failed";
    connect.disabled = false;
  }
}

async function stop() {
  const current = client;
  client = null;
  disconnect.disabled = true;
  if (current) {
    await current.close();
  }
  video.srcObject = null;
  playbackStatus.textContent = "Idle";
  peerStatus.textContent = "Peer: closed";
  signalingStatus.textContent = "Idle";
  connect.disabled = false;
}

function requiredElement(id: string) {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLElement)) {
    throw new Error(`Missing element #${id}`);
  }
  return element;
}

function requiredButton(id: string) {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLButtonElement)) {
    throw new Error(`Missing button #${id}`);
  }
  return element;
}

function requiredVideo(id: string) {
  const element = document.getElementById(id);
  if (!(element instanceof HTMLVideoElement)) {
    throw new Error(`Missing video #${id}`);
  }
  return element;
}
