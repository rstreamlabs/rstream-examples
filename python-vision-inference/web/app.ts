// See LICENSE file in the project root for license information.

import { z } from "zod";

// ---------------------------------------------------------------------------
// Server payload schemas. Everything that crosses the wire is parsed with zod
// before it is read, so a malformed status or response is caught at the edge
// rather than surfacing as an undefined access later.
// ---------------------------------------------------------------------------

const detectionCountSchema = z.object({
  label: z.string(),
  count: z.number().int(),
});

const workerSchema = z.object({
  name: z.string(),
  accelerator: z.string().nullish(),
  model: z.string().nullish(),
  device: z.string().nullish(),
});

type Worker = z.infer<typeof workerSchema>;

const statusSchema = z.object({
  worker: z.string().nullish(),
  pinned_worker: z.string().nullish(),
  workers: z.array(workerSchema).default([]),
  state: z.string().nullish(),
  model: z.string().nullish(),
  fps_source: z.number().nullish(),
  fps_detect: z.number().nullish(),
  infer_ms: z.number().nullish(),
  network_ms: z.number().nullish(),
  buffer_ms: z.number().nullish(),
  uplink_kbps: z.number().nullish(),
  detections: z.array(detectionCountSchema).default([]),
  detection_enabled: z.boolean().default(false),
  auto_suspended: z.boolean().default(false),
});

type Status = z.infer<typeof statusSchema>;

const detectionResponseSchema = z.object({
  detection_enabled: z.boolean(),
});

const pinResponseSchema = z.object({
  pinned_worker: z.string().nullable(),
});

// ---------------------------------------------------------------------------
// Typed element lookups. instanceof narrows without a cast and fails loudly if
// the markup and the script drift apart.
// ---------------------------------------------------------------------------

function requiredElement<T extends HTMLElement>(
  id: string,
  ctor: abstract new (...args: never[]) => T,
): T {
  const element = document.getElementById(id);
  if (!(element instanceof ctor)) {
    throw new Error(`missing element #${id}`);
  }
  return element;
}

const canvasEl = requiredElement("stream", HTMLCanvasElement);
const workerEl = requiredElement("worker", HTMLElement);
const stateEl = requiredElement("state", HTMLElement);
const modelEl = requiredElement("model", HTMLElement);
const fpsSourceEl = requiredElement("fps-source", HTMLElement);
const fpsDetectEl = requiredElement("fps-detect", HTMLElement);
const inferEl = requiredElement("infer", HTMLElement);
const networkEl = requiredElement("network", HTMLElement);
const bufferEl = requiredElement("buffer", HTMLElement);
const uplinkEl = requiredElement("uplink", HTMLElement);
const workersEl = requiredElement("workers", HTMLUListElement);
const selectionEl = requiredElement("selection", HTMLElement);
const detectionsEl = requiredElement("detections", HTMLUListElement);
const detectionsSummaryEl = requiredElement("detections-summary", HTMLElement);
const toggleEl = requiredElement("toggle-detection", HTMLButtonElement);

function canvasContext(canvas: HTMLCanvasElement): CanvasRenderingContext2D {
  const ctx = canvas.getContext("2d");
  if (ctx === null) {
    throw new Error("2d canvas context unavailable");
  }
  return ctx;
}

const canvasCtx = canvasContext(canvasEl);

// ---------------------------------------------------------------------------
// Buffered player: a browser-side jitter buffer for the frame stream.
//
// The device stamps every frame with its capture time. Frames are decoded as
// they arrive and queued, and a playback clock renders them at their true
// cadence behind a small adaptive delay. Network jitter shallower than the
// buffer becomes invisible; sustained underruns grow the buffer.
//
// The stream is a plain byte stream of `X-Frame-Ts` / `Content-Length` header
// blocks followed by JPEG bytes. It deliberately does not claim to be
// multipart/x-mixed-replace: WebKit special-cases that content type at the
// network layer and fails fetch() reads of it without a console error.
// ---------------------------------------------------------------------------

interface QueuedFrame {
  bitmap: ImageBitmap;
  ts: number; // device-time milliseconds
}

interface PlayerState {
  queue: QueuedFrame[];
  anchor: { deviceTs: number; wallTs: number } | null;
  current: ImageBitmap | null;
  bufferTargetMs: number;
  lastFrameAt: number;
}

const MAX_BUFFER_MS = 1500;
const MAX_QUEUED_FRAMES = 240;
const FRAME_SEPARATOR = [13, 10, 13, 10]; // \r\n\r\n

const player: PlayerState = {
  queue: [],
  anchor: null,
  current: null,
  bufferTargetMs: 400,
  lastFrameAt: 0,
};

function resetPlayer(): void {
  for (const frame of player.queue.splice(0)) {
    frame.bitmap.close();
  }
  player.anchor = null;
}

async function pumpStream(): Promise<void> {
  const decoder = new TextDecoder();
  for (;;) {
    try {
      const response = await fetch("/stream", { cache: "no-store" });
      if (response.body === null) {
        throw new Error("stream response has no body");
      }
      const reader = response.body.getReader();
      let pending = new Uint8Array(0);
      for (;;) {
        const { value, done } = await reader.read();
        if (done) {
          break;
        }
        const merged = new Uint8Array(pending.length + value.length);
        merged.set(pending);
        merged.set(value, pending.length);
        pending = await extractFrames(merged, decoder);
      }
    } catch {
      // connection lost: fall through to reconnect
    }
    resetPlayer();
    await delay(1000);
  }
}

async function extractFrames(
  buffer: Uint8Array<ArrayBuffer>,
  decoder: TextDecoder,
): Promise<Uint8Array<ArrayBuffer>> {
  let rest = buffer;
  for (;;) {
    const headerEnd = indexOfSequence(rest, FRAME_SEPARATOR);
    if (headerEnd < 0) {
      return rest;
    }
    const headers = decoder.decode(rest.subarray(0, headerEnd));
    const length = Number(/Content-Length: (\d+)/.exec(headers)?.[1] ?? NaN);
    const ts = Number(/X-Frame-Ts: (\d+)/.exec(headers)?.[1] ?? NaN);
    const body = headerEnd + FRAME_SEPARATOR.length;
    if (!Number.isFinite(length) || rest.length < body + length) {
      return rest;
    }
    if (Number.isFinite(ts)) {
      await enqueueFrame(rest.slice(body, body + length), ts);
    }
    rest = rest.subarray(body + length);
  }
}

async function enqueueFrame(
  jpeg: Uint8Array<ArrayBuffer>,
  ts: number,
): Promise<void> {
  const bitmap = await createImageBitmap(
    new Blob([jpeg], { type: "image/jpeg" }),
  );
  player.queue.push({ bitmap, ts });
  player.lastFrameAt = performance.now();
  while (player.queue.length > MAX_QUEUED_FRAMES) {
    player.queue.shift()?.bitmap.close();
  }
}

function indexOfSequence(haystack: Uint8Array, needle: number[]): number {
  outer: for (let i = 0; i <= haystack.length - needle.length; i += 1) {
    for (let j = 0; j < needle.length; j += 1) {
      if (haystack[i + j] !== needle[j]) {
        continue outer;
      }
    }
    return i;
  }
  return -1;
}

function renderPlayer(): void {
  requestAnimationFrame(renderPlayer);
  const now = performance.now();
  if (player.anchor === null) {
    const head = player.queue[0];
    if (head === undefined) {
      return;
    }
    player.anchor = { deviceTs: head.ts, wallTs: now + player.bufferTargetMs };
  }
  const playTs = player.anchor.deviceTs + (now - player.anchor.wallTs);
  let next: QueuedFrame | null = null;
  while (player.queue.length > 0 && player.queue[0].ts <= playTs) {
    next?.bitmap.close();
    next = player.queue.shift() ?? null;
  }
  if (next !== null) {
    drawFrame(next.bitmap);
    return;
  }
  growBufferIfStarved(now, playTs);
}

function drawFrame(bitmap: ImageBitmap): void {
  if (canvasEl.width !== bitmap.width) {
    canvasEl.width = bitmap.width;
    canvasEl.height = bitmap.height;
  }
  canvasCtx.drawImage(bitmap, 0, 0);
  player.current?.close();
  player.current = bitmap;
}

function growBufferIfStarved(now: number, playTs: number): void {
  // Underrun while frames are still flowing: the buffer was too shallow for
  // the observed jitter. Deepen it and re-anchor on the next frame.
  if (player.anchor === null || player.queue.length > 0) {
    return;
  }
  const sinceLastFrame = now - player.lastFrameAt;
  if (sinceLastFrame >= 5000) {
    return;
  }
  if (playTs > player.anchor.deviceTs && sinceLastFrame > 300) {
    player.bufferTargetMs = Math.min(
      MAX_BUFFER_MS,
      Math.round(player.bufferTargetMs * 1.25),
    );
    player.anchor = null;
  }
}

void pumpStream();
renderPlayer();

// ---------------------------------------------------------------------------
// Status panel and detection toggle. The toggle applies the latest intent:
// clicks during a transition are never swallowed, the final state wins.
// ---------------------------------------------------------------------------

interface ToggleState {
  lastEnabled: boolean | null;
  wantEnabled: boolean | null;
  senderBusy: boolean;
}

const toggle: ToggleState = {
  lastEnabled: null,
  wantEnabled: null,
  senderBusy: false,
};

// Pin state mirrors the toggle's last/want/busy reconciliation, with one extra
// value: `wantPinned === undefined` means no pending intent, `null` means the
// user wants automatic selection, a string means that worker.
interface PinState {
  lastPinned: string | null;
  wantPinned: string | null | undefined;
  senderBusy: boolean;
}

const pin: PinState = {
  lastPinned: null,
  wantPinned: undefined,
  senderBusy: false,
};

const effectivePinned = (): string | null =>
  pin.wantPinned !== undefined ? pin.wantPinned : pin.lastPinned;

// The most recent status, kept so a click can refresh the selection line
// immediately rather than waiting for the next server frame.
let latestStatus: Status | null = null;

const delay = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

const dash = (value: number | null | undefined, suffix: string): string =>
  value == null ? "–" : `${value}${suffix}`;

const chip = (active: boolean): string =>
  "inline-flex items-center gap-2 rounded-md border border-[#171512]/15 px-3 py-1 " +
  (active ? "bg-[#171512] text-white" : "bg-[#f5f2e9] text-[#171512]");

function mutedItem(text: string): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "text-[#4f4a43]";
  li.textContent = text;
  return li;
}

function renderButton(status: Status): void {
  toggle.lastEnabled = status.detection_enabled;
  const poolEmpty = status.workers.length === 0;
  if (
    toggle.wantEnabled !== null &&
    toggle.wantEnabled !== toggle.lastEnabled
  ) {
    toggleEl.textContent = toggle.wantEnabled ? "Enabling…" : "Disabling…";
    toggleEl.disabled = false; // clicking again flips the queued intent
    return;
  }
  toggle.wantEnabled = null;
  if (status.detection_enabled) {
    toggleEl.textContent = "Disable detection";
    toggleEl.disabled = false;
    toggleEl.title = "";
  } else {
    toggleEl.textContent = "Enable detection";
    toggleEl.disabled = poolEmpty;
    toggleEl.title = poolEmpty ? "No workers in the pool" : "";
  }
}

function workerMeta(worker: Worker): string {
  const accelerator =
    worker.accelerator && worker.device
      ? `${worker.accelerator} (${worker.device})`
      : worker.accelerator;
  return [accelerator, worker.model]
    .filter((value): value is string => Boolean(value))
    .join(" · ");
}

// The worker list is the one interactive, frequently-updated region, so it is
// rebuilt only when its visible content actually changes. Status arrives several
// times a second (FPS, inference time…); rebuilding the chips on every frame
// would destroy and recreate the buttons under the user's cursor, dropping any
// click whose mousedown and mouseup straddle a rebuild. The signature is built
// from the server-confirmed pin, not the optimistic intent, so a burst of
// clicks never churns the DOM mid-interaction.
let workersSignature = "";

function renderSelection(status: Status): void {
  const pinned = effectivePinned();
  if (pinned === null) {
    selectionEl.textContent = "Selecting workers automatically.";
  } else if (status.worker === pinned) {
    selectionEl.textContent = `Pinned to ${pinned}.`;
  } else {
    selectionEl.textContent = `Pinning to ${pinned}…`;
  }
}

function renderWorkers(status: Status): void {
  renderSelection(status);
  const confirmedPinned = status.pinned_worker ?? null;
  const signature = JSON.stringify([
    status.worker ?? null,
    confirmedPinned,
    status.workers.map((w) => [w.name, w.accelerator, w.model, w.device]),
  ]);
  if (signature === workersSignature) {
    return; // nothing the pool view shows changed; keep the live buttons stable
  }
  workersSignature = signature;
  if (status.workers.length === 0) {
    workersEl.replaceChildren(mutedItem("No workers in the pool."));
    return;
  }
  workersEl.replaceChildren(
    ...status.workers.map((worker) =>
      workerRow(worker, status, confirmedPinned),
    ),
  );
}

function workerRow(
  worker: Worker,
  status: Status,
  pinned: string | null,
): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "flex flex-wrap items-center gap-x-2 gap-y-1";
  const button = document.createElement("button");
  button.type = "button";
  const isPinned = worker.name === pinned;
  // The click is handled by a single delegated listener on the stable <ul>;
  // the name travels on the element so a rebuild can never orphan a handler.
  button.dataset.worker = worker.name;
  button.className = chip(worker.name === status.worker) + " cursor-pointer";
  if (isPinned) {
    button.className += " ring-2 ring-[#171512]/40 ring-offset-1";
  }
  button.textContent = worker.name;
  button.title = isPinned
    ? "Pinned — click to return to automatic selection"
    : "Click to pin the session to this worker";
  li.appendChild(button);
  const meta = workerMeta(worker);
  if (meta !== "") {
    const metaEl = document.createElement("span");
    metaEl.className = "text-xs text-[#6b665c]";
    metaEl.textContent = meta;
    li.appendChild(metaEl);
  }
  if (isPinned) {
    const tag = document.createElement("span");
    tag.className = "text-[11px] font-medium uppercase text-[#6b665c]";
    tag.textContent = "pinned";
    li.appendChild(tag);
  }
  return li;
}

// Delegated on the <ul>, which is never replaced, so the handler survives every
// chip rebuild — unlike a per-button listener.
workersEl.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof HTMLElement)) {
    return;
  }
  const button = target.closest("button[data-worker]");
  if (button instanceof HTMLButtonElement && button.dataset.worker) {
    onWorkerClick(button.dataset.worker);
  }
});

function onWorkerClick(name: string): void {
  // Click the pinned worker again to return to automatic selection.
  pin.wantPinned = effectivePinned() === name ? null : name;
  if (latestStatus !== null) {
    renderSelection(latestStatus); // instant feedback, before the next frame
  }
  void reconcilePin();
}

async function postPin(
  worker: string | null,
): Promise<string | null | undefined> {
  // Returns the confirmed pin (a name or null), or undefined on failure.
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      const response = await fetch("/pin", {
        method: "POST",
        cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ worker }),
        signal: AbortSignal.timeout(2500),
      });
      if (response.ok) {
        return pinResponseSchema.parse(await response.json()).pinned_worker;
      }
    } catch {
      // fall through to retry
    }
    await delay(200);
  }
  return undefined;
}

async function reconcilePin(): Promise<void> {
  if (pin.senderBusy) {
    return;
  }
  pin.senderBusy = true;
  try {
    while (pin.wantPinned !== undefined && pin.wantPinned !== pin.lastPinned) {
      const target = pin.wantPinned;
      const confirmed = await postPin(target);
      if (confirmed === undefined) {
        pin.wantPinned = undefined;
        break;
      }
      pin.lastPinned = confirmed;
      if (pin.wantPinned === target && confirmed === target) {
        pin.wantPinned = undefined;
      }
    }
  } finally {
    pin.senderBusy = false;
  }
}

function renderDetections(status: Status): void {
  const total = status.detections.reduce((sum, det) => sum + det.count, 0);
  if (!status.detection_enabled) {
    detectionsSummaryEl.textContent = status.auto_suspended
      ? "No active session"
      : "Detection is disabled";
    detectionsEl.replaceChildren();
    return;
  }
  if (status.state !== "connected") {
    detectionsSummaryEl.textContent = "No active session";
    detectionsEl.replaceChildren();
    return;
  }
  detectionsSummaryEl.textContent =
    total === 0
      ? "Nothing detected in the current frame"
      : `${total} object${total > 1 ? "s" : ""} tracked`;
  detectionsEl.replaceChildren(
    ...status.detections.map((det) => {
      const li = document.createElement("li");
      li.className =
        "flex items-center justify-between rounded-md border border-[#171512]/15 bg-[#f5f2e9] px-3 py-2";
      const label = document.createElement("span");
      label.className = "font-medium text-[#171512]";
      label.textContent = det.label;
      const count = document.createElement("span");
      count.className = "text-xs text-[#6b665c]";
      count.textContent = `×${det.count}`;
      li.append(label, count);
      return li;
    }),
  );
}

function render(status: Status): void {
  latestStatus = status;
  workerEl.textContent =
    status.worker ??
    (status.detection_enabled || status.auto_suspended
      ? "Waiting for a worker…"
      : "Detection disabled");
  stateEl.textContent = status.state ?? "starting";
  modelEl.textContent = status.model ?? "–";
  fpsSourceEl.textContent = dash(status.fps_source, " fps");
  fpsDetectEl.textContent = dash(status.fps_detect, " fps");
  inferEl.textContent = dash(status.infer_ms, " ms");
  networkEl.textContent = dash(status.network_ms, " ms");
  bufferEl.textContent = dash(status.buffer_ms, " ms");
  uplinkEl.textContent = dash(status.uplink_kbps, " kbps");
  pin.lastPinned = status.pinned_worker ?? null;
  if (pin.wantPinned !== undefined && pin.wantPinned === pin.lastPinned) {
    pin.wantPinned = undefined;
  }
  renderButton(status);
  renderWorkers(status);
  renderDetections(status);
}

async function postDetection(enabled: boolean): Promise<boolean | null> {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      const response = await fetch("/detection", {
        method: "POST",
        cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
        // A request landing on a dead keep-alive connection would otherwise
        // hang for the TCP timeout; abort fast and retry on a fresh one.
        signal: AbortSignal.timeout(2500),
      });
      if (response.ok) {
        return detectionResponseSchema.parse(await response.json())
          .detection_enabled;
      }
    } catch {
      // fall through to retry
    }
    await delay(200);
  }
  return null;
}

async function reconcileDetection(): Promise<void> {
  if (toggle.senderBusy) {
    return;
  }
  toggle.senderBusy = true;
  try {
    while (
      toggle.wantEnabled !== null &&
      toggle.wantEnabled !== toggle.lastEnabled
    ) {
      const target = toggle.wantEnabled;
      const confirmed = await postDetection(target);
      if (confirmed === null) {
        toggle.wantEnabled = null;
        break;
      }
      toggle.lastEnabled = confirmed;
      if (toggle.wantEnabled === target && confirmed === target) {
        toggle.wantEnabled = null;
      }
    }
  } finally {
    toggle.senderBusy = false;
    toggleEl.textContent = toggle.lastEnabled
      ? "Disable detection"
      : "Enable detection";
    toggleEl.disabled = false;
  }
}

toggleEl.addEventListener("click", () => {
  if (toggle.lastEnabled === null) {
    return;
  }
  const shown = toggle.wantEnabled ?? toggle.lastEnabled;
  toggle.wantEnabled = !shown;
  toggleEl.textContent = toggle.wantEnabled ? "Enabling…" : "Disabling…";
  void reconcileDetection();
});

function renderOffline(): void {
  workerEl.textContent = "Device unreachable";
  stateEl.textContent = "offline";
  detectionsSummaryEl.textContent = "No connection to the device";
  detectionsEl.replaceChildren();
  toggleEl.textContent = "Device offline";
  toggleEl.disabled = true;
  // Force a rebuild on the first frame after reconnecting.
  workersSignature = "";
}

const events = new EventSource("/events");
events.onmessage = (event: MessageEvent<string>) => {
  const parsed = statusSchema.safeParse(JSON.parse(event.data));
  if (parsed.success) {
    render(parsed.data);
  }
};
// EventSource reconnects by itself; until it does, the device is unreachable
// and every control on this page is meaningless.
events.onerror = () => {
  renderOffline();
};
