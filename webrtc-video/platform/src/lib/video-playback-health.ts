export type PlaybackHealthSample = {
  active: boolean
  decodedFrames: number
  timeMilliseconds: number
}

type PlaybackHealthTrackerOptions = {
  minimumFramesPerSecond?: number
  startupGraceMilliseconds?: number
  unhealthyMilliseconds?: number
}

const defaultMinimumFramesPerSecond = 5
const defaultStartupGraceMilliseconds = 8000
const defaultUnhealthyMilliseconds = 8000
const minimumFrameRateRatio = 0.2
const absoluteMinimumFramesPerSecond = 0.2

export function minimumUsableFramesPerSecond(expectedFramesPerSecond: unknown) {
  if (
    typeof expectedFramesPerSecond !== "number" ||
    !Number.isFinite(expectedFramesPerSecond) ||
    expectedFramesPerSecond <= 0
  ) {
    return defaultMinimumFramesPerSecond
  }
  return Math.max(
    absoluteMinimumFramesPerSecond,
    expectedFramesPerSecond * minimumFrameRateRatio,
  )
}

export class PlaybackHealthTracker {
  private readonly minimumFramesPerSecond: number
  private readonly startupGraceMilliseconds: number
  private readonly unhealthyMilliseconds: number
  private activeSince: number | null = null
  private lastFrames: number | null = null
  private lastTime: number | null = null
  private unhealthySince: number | null = null

  constructor(options: PlaybackHealthTrackerOptions = {}) {
    this.minimumFramesPerSecond = positiveNumber(
      options.minimumFramesPerSecond ?? defaultMinimumFramesPerSecond,
      "Minimum playback frame rate",
    )
    this.startupGraceMilliseconds = nonNegativeInteger(
      options.startupGraceMilliseconds ?? defaultStartupGraceMilliseconds,
      "Playback startup grace period",
    )
    this.unhealthyMilliseconds = positiveInteger(
      options.unhealthyMilliseconds ?? defaultUnhealthyMilliseconds,
      "Playback unhealthy period",
    )
  }

  observe(sample: PlaybackHealthSample) {
    validateSample(sample)
    if (!sample.active) {
      this.reset()
      return false
    }
    if (
      this.activeSince === null ||
      this.lastFrames === null ||
      this.lastTime === null ||
      sample.timeMilliseconds <= this.lastTime ||
      sample.decodedFrames < this.lastFrames
    ) {
      this.activeSince = sample.timeMilliseconds
      this.lastFrames = sample.decodedFrames
      this.lastTime = sample.timeMilliseconds
      this.unhealthySince = null
      return false
    }
    const elapsedMilliseconds = sample.timeMilliseconds - this.lastTime
    const decodedFrames = sample.decodedFrames - this.lastFrames
    const framesPerSecond = (decodedFrames * 1000) / elapsedMilliseconds
    this.lastFrames = sample.decodedFrames
    this.lastTime = sample.timeMilliseconds
    if (
      sample.timeMilliseconds - this.activeSince <
      this.startupGraceMilliseconds
    ) {
      return false
    }
    if (framesPerSecond >= this.minimumFramesPerSecond) {
      this.unhealthySince = null
      return false
    }
    this.unhealthySince ??= sample.timeMilliseconds
    return (
      sample.timeMilliseconds - this.unhealthySince >=
      this.unhealthyMilliseconds
    )
  }

  reset() {
    this.activeSince = null
    this.lastFrames = null
    this.lastTime = null
    this.unhealthySince = null
  }
}

function validateSample(sample: PlaybackHealthSample) {
  nonNegativeNumber(sample.decodedFrames, "Decoded frame count")
  nonNegativeNumber(sample.timeMilliseconds, "Playback sample time")
}

function positiveNumber(value: number, name: string) {
  if (!Number.isFinite(value) || value <= 0) {
    throw new Error(`${name} must be a positive number`)
  }
  return value
}

function nonNegativeNumber(value: number, name: string) {
  if (!Number.isFinite(value) || value < 0) {
    throw new Error(`${name} must be a non-negative number`)
  }
  return value
}

function positiveInteger(value: number, name: string) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${name} must be a positive integer`)
  }
  return value
}

function nonNegativeInteger(value: number, name: string) {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${name} must be a non-negative integer`)
  }
  return value
}
