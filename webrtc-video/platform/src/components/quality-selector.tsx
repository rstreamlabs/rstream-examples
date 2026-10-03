"use client"

import { useEffect, useRef, useState } from "react"
import {
  QualityClient,
  type QualityState,
} from "../../../shared/quality-client"

export function QualitySelector({ deviceId }: { deviceId: string }) {
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
    <div className="space-y-1 text-sm">
      <label className="flex flex-wrap items-center gap-3">
        Source quality
        <select
          aria-label="Source quality"
          className="rounded-md border border-border bg-background px-3 py-2"
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
      </label>
      <p className="text-xs text-muted-foreground">
        Applies to all viewers of this device. The source can reduce its bitrate
        when the uplink is congested.
      </p>
      {state.activeEncoders > 0 ? (
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
  )
}
