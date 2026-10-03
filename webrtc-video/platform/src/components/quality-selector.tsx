"use client"

import { useEffect, useRef, useState } from "react"
import { ChevronDown } from "lucide-react"
import {
  QualityClient,
  type QualityState,
} from "../../../shared/quality-client"

export function QualitySelector({
  deviceId,
  compact = false,
}: {
  deviceId: string
  compact?: boolean
}) {
  const client = useRef<QualityClient | null>(null)
  const [state, setState] = useState<QualityState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    setState(null)
    setError(null)
    setBusy(false)
    const instance = new QualityClient({
      url: () => `/api/devices/${encodeURIComponent(deviceId)}/quality`,
      onState: (value) => {
        if (active) {
          setState(value)
          setError(null)
        }
      },
      onError: (error) => {
        if (active) setError(error.message)
      },
    })
    client.current = instance
    instance.start()
    return () => {
      active = false
      instance.stop()
      if (client.current === instance) client.current = null
    }
  }, [deviceId])
  if (!state) return null
  return (
    <div className="source-quality min-w-0 text-sm">
      <label className="flex min-w-0 items-center gap-3">
        <span className="hidden shrink-0 sm:inline">Source quality</span>
        <span className="shrink-0 sm:hidden">Quality</span>
        <span className="relative min-w-0 flex-1 sm:max-w-56">
          <select
            aria-label="Source quality"
            title="Source quality — applies to all viewers"
            className="h-10 w-full cursor-pointer appearance-none truncate rounded-md border border-input bg-background pl-3 pr-10 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60"
            value={state.selected}
            disabled={busy || !!error}
            onChange={async (event) => {
              const instance = client.current
              if (!instance) return
              setBusy(true)
              try {
                await instance.select(event.target.value)
              } finally {
                if (client.current === instance) setBusy(false)
              }
            }}
          >
            {state.modes.map((mode) => (
              <option key={mode.id} value={mode.id}>
                {mode.label}
                {mode.bitrateKbps
                  ? ` · ${mode.bitrateKbps / 1000} Mbit/s max`
                  : ""}
              </option>
            ))}
          </select>
          <ChevronDown
            aria-hidden
            className="pointer-events-none absolute right-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
          />
        </span>
      </label>
      {!compact || state.failedUpdates > 0 || error ? (
        <div className="source-quality-details grid gap-2">
          {!compact ? (
            <p className="text-xs text-muted-foreground">
              Applies to all viewers of this device. The source can reduce its
              bitrate when the uplink is congested.
            </p>
          ) : null}
          {state.activeEncoders > 0 && !compact ? (
            <p className="text-xs text-muted-foreground">
              Encoder target: {state.minAppliedBitrateKbps / 1000}
              {state.minAppliedBitrateKbps !== state.maxAppliedBitrateKbps
                ? `–${state.maxAppliedBitrateKbps / 1000}`
                : ""}{" "}
              Mbit/s.
            </p>
          ) : null}
          {state.failedUpdates > 0 ? (
            <p className="text-xs text-destructive">
              The encoder reported quality update failures. Check producer
              diagnostics.
            </p>
          ) : null}
          {error ? (
            <p role="status" className="text-xs text-destructive">
              {error}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
