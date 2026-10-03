export type QualityMode = {
  id: string;
  label: string;
  bitrateKbps: number;
  sourceProfile?: string;
};
export type QualityFormatProfile = {
  id: string;
  width: number;
  height: number;
  frameRate: { numerator: number; denominator: number };
  requestedEncoders: number;
  observedEncoders: number;
};
export type QualityFormatState = {
  defaultProfile: string;
  adaptive: boolean;
  activeEncoders: number;
  pendingEncoders: number;
  unconfirmedEncoders: number;
  failedUpdates: number;
  profiles: QualityFormatProfile[];
};
export type QualityState = {
  modes: QualityMode[];
  selected: string;
  version: string;
  activeEncoders: number;
  minAppliedBitrateKbps: number;
  maxAppliedBitrateKbps: number;
  failedUpdates: number;
  sourceFormat?: QualityFormatState;
};

// Describe confirmed encoder caps, independently of the selected policy and
// measured playback cadence. Never present a requested profile as observed.
export function sourceFormatDescription(state: QualityState): string | null {
  const format = state.sourceFormat;
  if (!format) return null;
  if (!format.activeEncoders)
    return "Source format applies when streaming starts.";
  const observed = format.profiles.filter(
    (profile) => profile.observedEncoders > 0,
  );
  let description = "Source format awaiting confirmation.";
  if (observed.length > 1) {
    description = "Source formats vary between active connections.";
  } else if (observed.length === 1 && !format.unconfirmedEncoders) {
    const profile = observed[0];
    const fps =
      Math.round(
        (profile.frameRate.numerator / profile.frameRate.denominator) * 100,
      ) / 100;
    description = `Source format: ${profile.width} × ${profile.height} · ${fps} fps.`;
  }
  return format.pendingEncoders ? `${description} Updating…` : description;
}

const count = (value: unknown, maximum: number) =>
  Number.isSafeInteger(value) &&
  (value as number) >= 0 &&
  (value as number) <= maximum;
const profileID = (value: unknown): value is string =>
  typeof value === "string" && /^[a-z][a-z0-9-]{0,31}$/.test(value);

function validSourceFormat(value: QualityFormatState): boolean {
  if (
    !value ||
    typeof value !== "object" ||
    !profileID(value.defaultProfile) ||
    typeof value.adaptive !== "boolean" ||
    !count(value.activeEncoders, 100_000) ||
    !count(value.pendingEncoders, value.activeEncoders) ||
    !count(value.unconfirmedEncoders, value.activeEncoders) ||
    !count(value.failedUpdates, Number.MAX_SAFE_INTEGER) ||
    !Array.isArray(value.profiles) ||
    value.profiles.length < 1 ||
    value.profiles.length > 16 ||
    !value.profiles.every((profile) => {
      const rate = profile?.frameRate;
      return (
        profile &&
        profileID(profile.id) &&
        profile.id !== "auto" &&
        count(profile.width, 16384) &&
        profile.width >= 2 &&
        profile.width % 2 === 0 &&
        count(profile.height, 16384) &&
        profile.height >= 2 &&
        profile.height % 2 === 0 &&
        rate &&
        count(rate.numerator, 1_000_000) &&
        rate.numerator > 0 &&
        count(rate.denominator, 1_000_000) &&
        rate.denominator > 0 &&
        rate.numerator <= 240 * rate.denominator &&
        count(profile.requestedEncoders, value.activeEncoders) &&
        count(profile.observedEncoders, value.activeEncoders)
      );
    }) ||
    new Set(value.profiles.map((profile) => profile.id)).size !==
      value.profiles.length ||
    !value.profiles.some((profile) => profile.id === value.defaultProfile)
  )
    return false;
  return (
    value.profiles.reduce(
      (sum, profile) => sum + profile.requestedEncoders,
      0,
    ) <= value.activeEncoders &&
    value.profiles.reduce((sum, profile) => sum + profile.observedEncoders, 0) +
      value.unconfirmedEncoders ===
      value.activeEncoders
  );
}

export function parseQualityState(value: unknown): QualityState {
  if (!value || typeof value !== "object")
    throw new Error("Invalid source quality response.");
  const state = value as QualityState;
  if (
    !Array.isArray(state.modes) ||
    state.modes.length < 2 ||
    state.modes.length > 17 ||
    !state.modes.every(
      (mode) =>
        mode &&
        profileID(mode.id) &&
        typeof mode.label === "string" &&
        mode.label.length > 0 &&
        mode.label.length <= 80 &&
        count(mode.bitrateKbps, 50_000),
    ) ||
    new Set(state.modes.map((mode) => mode.id)).size !== state.modes.length ||
    !state.modes.some((mode) => mode.id === "auto" && mode.bitrateKbps === 0) ||
    !state.modes.some((mode) => mode.id === state.selected) ||
    typeof state.version !== "string" ||
    !/^[a-f0-9]{32}:[0-9]{1,20}$/.test(state.version) ||
    !count(state.activeEncoders, 100_000) ||
    !count(state.minAppliedBitrateKbps, 50_000) ||
    !count(state.maxAppliedBitrateKbps, 50_000) ||
    !count(state.failedUpdates, Number.MAX_SAFE_INTEGER) ||
    state.minAppliedBitrateKbps > state.maxAppliedBitrateKbps
  )
    throw new Error("Invalid source quality response.");
  if (
    (state.sourceFormat !== undefined &&
      !validSourceFormat(state.sourceFormat)) ||
    state.modes.some(
      (mode) =>
        mode.sourceProfile !== undefined &&
        (mode.id === "auto" ||
          !profileID(mode.sourceProfile) ||
          !state.sourceFormat?.profiles.some(
            (profile) => profile.id === mode.sourceProfile,
          )),
    )
  )
    throw new Error("Invalid source format response.");
  return state;
}

export class QualityRequestError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(
      status === 409
        ? "Source quality changed. Refresh and select again."
        : status === 401 || status === 403
          ? "Source quality access denied."
          : "Source quality is temporarily unavailable.",
    );
    this.status = status;
  }
}

// Shared by the embedded viewer and Next.js. One poll or mutation at a time;
// stop aborts I/O, removes timers, and suppresses stale callbacks after unmount.
export class QualityClient {
  private readonly options: {
    url: () => string;
    onState: (state: QualityState | null) => void;
    onError: (error: Error) => void;
    intervalMs?: number;
    timeoutMs?: number;
    fetch?: typeof fetch;
  };
  private state: QualityState | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private request: AbortController | null = null;
  private generation = 0;
  private stopped = false;
  private started = false;
  private selecting = false;

  constructor(options: QualityClient["options"]) {
    for (const value of [
      options.intervalMs ?? 5000,
      options.timeoutMs ?? 8000,
    ]) {
      if (!Number.isSafeInteger(value) || value < 1 || value > 60_000)
        throw new Error("Invalid quality request timing.");
    }
    this.options = options;
  }

  start() {
    if (this.started || this.stopped)
      throw new Error("Quality client cannot be started twice.");
    this.started = true;
    void this.poll();
  }

  stop() {
    this.stopped = true;
    this.generation++;
    this.request?.abort(new Error("Source quality client stopped"));
    this.clearTimer();
  }

  async select(mode: string) {
    if (this.stopped || this.selecting || !this.state) return;
    if (!this.state.modes.some((preset) => preset.id === mode))
      throw new Error("Unknown source quality mode.");
    this.selecting = true;
    this.clearTimer();
    const version = this.state.version;
    await this.perform({ mode, version });
    this.selecting = false;
    if (!this.stopped) void this.poll();
  }

  private async poll() {
    if (this.stopped || this.selecting) return;
    const generation = await this.perform();
    if (!this.stopped && !this.selecting && generation === this.generation)
      this.timer = setTimeout(() => {
        this.timer = null;
        void this.poll();
      }, this.options.intervalMs ?? 5000);
  }

  private clearTimer() {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
  }

  private async perform(selection?: { mode: string; version: string }) {
    this.request?.abort(new Error("Source quality request superseded"));
    const controller = new AbortController();
    this.request = controller;
    const generation = ++this.generation;
    const current = () => !this.stopped && generation === this.generation;
    const timeout = setTimeout(
      () => controller.abort(new Error("Source quality request timed out.")),
      this.options.timeoutMs ?? 8000,
    );
    try {
      const response = await (
        this.options.fetch ?? globalThis.fetch.bind(globalThis)
      )(this.options.url(), {
        method: selection ? "PUT" : "GET",
        headers: selection ? { "Content-Type": "application/json" } : {},
        body: selection ? JSON.stringify(selection) : undefined,
        cache: "no-store",
        redirect: "error",
        signal: controller.signal,
      });
      if (response.status === 204 || response.status === 404) {
        await response.body?.cancel();
        if (current()) {
          this.state = null;
          this.options.onState(null);
        }
        return generation;
      }
      if (!response.ok) {
        await response.body?.cancel();
        throw new QualityRequestError(response.status);
      }
      const reader = response.body?.getReader();
      if (!reader) throw new Error("Missing source quality response.");
      const chunks: Uint8Array[] = [];
      let size = 0;
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > 16 * 1024) {
            await reader.cancel();
            throw new Error("Source quality response is too large.");
          }
          chunks.push(value);
        }
      } finally {
        reader.releaseLock();
      }
      const body = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) {
        body.set(chunk, offset);
        offset += chunk.length;
      }
      const state = parseQualityState(
        JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body)),
      );
      if (current()) {
        this.state = state;
        this.options.onState(state);
      }
    } catch (error) {
      if (current())
        this.options.onError(
          error instanceof QualityRequestError
            ? error
            : new Error("Source quality is temporarily unavailable."),
        );
    } finally {
      clearTimeout(timeout);
      if (this.request === controller) this.request = null;
    }
    return generation;
  }
}
