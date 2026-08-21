type CancelTimer = (timer: number) => void;
type ScheduleTimer = (
  callback: () => void,
  delayMilliseconds: number,
) => number;

export type ICERestartDisposition = "connected" | "pending" | "retry";

export class ICERestartTimers {
  private readonly cancelTimer: CancelTimer;
  private outcomeTimer: number | null = null;
  private retryTimer: number | null = null;
  private readonly scheduleTimer: ScheduleTimer;

  constructor(
    scheduleTimer: ScheduleTimer = (callback, delayMilliseconds) =>
      window.setTimeout(callback, delayMilliseconds),
    cancelTimer: CancelTimer = (timer) => window.clearTimeout(timer),
  ) {
    this.scheduleTimer = scheduleTimer;
    this.cancelTimer = cancelTimer;
  }

  get retryScheduled() {
    return this.retryTimer !== null;
  }

  scheduleOutcome(callback: () => void, delayMilliseconds: number) {
    this.clearOutcome();
    this.outcomeTimer = this.scheduleTimer(() => {
      this.outcomeTimer = null;
      callback();
    }, delayMilliseconds);
  }

  scheduleRetry(callback: () => void, delayMilliseconds: number) {
    if (this.retryTimer !== null) {
      return false;
    }
    this.retryTimer = this.scheduleTimer(() => {
      this.retryTimer = null;
      callback();
    }, delayMilliseconds);
    return true;
  }

  clearOutcome() {
    if (this.outcomeTimer !== null) {
      this.cancelTimer(this.outcomeTimer);
      this.outcomeTimer = null;
    }
  }

  clear() {
    this.clearOutcome();
    if (this.retryTimer !== null) {
      this.cancelTimer(this.retryTimer);
      this.retryTimer = null;
    }
  }
}

export function iceRestartDisposition(
  connectionState: RTCPeerConnectionState,
  iceState: RTCIceConnectionState,
): ICERestartDisposition {
  if (
    connectionState === "connected" &&
    (iceState === "connected" || iceState === "completed")
  ) {
    return "connected";
  }
  if (
    connectionState === "failed" ||
    iceState === "disconnected" ||
    iceState === "failed"
  ) {
    return "retry";
  }
  return "pending";
}
