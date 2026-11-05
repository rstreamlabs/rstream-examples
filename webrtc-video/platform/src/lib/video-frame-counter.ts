type FrameVideo = {
  requestVideoFrameCallback?: (callback: VideoFrameRequestCallback) => number
  cancelVideoFrameCallback?: (handle: number) => void
  getVideoPlaybackQuality?: () => VideoPlaybackQuality
  webkitDecodedFrameCount?: number
}

// Safari can return zero totalVideoFrames for a playing MediaStream. Prefer
// the compositor's presented-frame count; keep the legacy path for browsers
// without frame callbacks. An absent measurement never means zero frames.
export function createVideoFrameCounter(video: FrameVideo) {
  let frames: number | null = null
  let handle: number | undefined
  let stopped = false
  const callbacks =
    typeof video.requestVideoFrameCallback === "function" &&
    typeof video.cancelVideoFrameCallback === "function"
  const next: VideoFrameRequestCallback = (_now, metadata) => {
    if (stopped) return
    if (Number.isFinite(metadata.presentedFrames))
      frames = metadata.presentedFrames
    handle = video.requestVideoFrameCallback?.(next)
  }
  if (callbacks) handle = video.requestVideoFrameCallback?.(next)
  return {
    read: () => {
      if (callbacks) return frames
      try {
        const quality = video.getVideoPlaybackQuality?.()
        if (quality && Number.isFinite(quality.totalVideoFrames))
          return quality.totalVideoFrames
        return Number.isFinite(video.webkitDecodedFrameCount)
          ? Number(video.webkitDecodedFrameCount)
          : null
      } catch {
        return null
      }
    },
    stop: () => {
      if (stopped) return
      stopped = true
      if (handle !== undefined) video.cancelVideoFrameCallback?.(handle)
    },
  }
}
