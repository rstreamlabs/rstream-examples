"use client"

import { useEffect, useState } from "react"
import { z } from "zod"

const metricsSchema = z
  .object({
    state: z.enum(["ready", "idle"]),
    readers: z.number().int().nonnegative(),
    inboundBitsPerSecond: z.number().finite().nonnegative().nullable(),
    outboundBitsPerSecond: z.number().finite().nonnegative().nullable(),
    sampledAt: z.iso.datetime(),
    intervalMs: z.number().int().min(2000).max(15000).nullable(),
  })
  .strict()

export function DistributionMetrics({ deviceId }: { deviceId: string }) {
  const [metrics, setMetrics] = useState<z.infer<typeof metricsSchema> | null>(
    null,
  )
  const [unavailable, setUnavailable] = useState(false)
  useEffect(() => {
    let disposed = false,
      disabled = false,
      refreshOnFinish = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let current: AbortController | undefined
    setMetrics(null)
    setUnavailable(false)
    async function refresh() {
      if (disposed || disabled || document.hidden || current) return
      const controller = new AbortController()
      current = controller
      const deadline = setTimeout(() => controller.abort(), 5000)
      try {
        const response = await fetch(
          `/api/devices/${encodeURIComponent(deviceId)}/metrics`,
          {
            signal: controller.signal,
            cache: "no-store",
            redirect: "error",
          },
        )
        if (response.status === 204) {
          disabled = true
          if (!disposed) {
            setMetrics(null)
            setUnavailable(false)
          }
          return
        }
        if ([401, 403, 404].includes(response.status)) disabled = true
        if (!response.ok) {
          await response.body?.cancel()
          throw new Error("Unavailable")
        }
        const value = metricsSchema.parse(await response.json())
        if (!disposed && !document.hidden) {
          setMetrics(value)
          setUnavailable(false)
        }
      } catch {
        if (!disposed && !document.hidden) {
          setMetrics(null)
          setUnavailable(true)
        }
      } finally {
        clearTimeout(deadline)
        current = undefined
        if (!disposed && !disabled && !document.hidden) {
          timer = setTimeout(refresh, refreshOnFinish ? 0 : 5000)
          refreshOnFinish = false
        }
      }
    }
    const onVisibility = () => {
      clearTimeout(timer)
      if (document.hidden) {
        current?.abort()
        setMetrics(null)
      } else if (current) refreshOnFinish = true
      else void refresh()
    }
    document.addEventListener("visibilitychange", onVisibility)
    void refresh()
    return () => {
      disposed = true
      clearTimeout(timer)
      current?.abort()
      document.removeEventListener("visibilitychange", onVisibility)
    }
  }, [deviceId])
  if (unavailable)
    return (
      <p className="text-xs text-muted-foreground" role="status">
        Distribution metrics unavailable.
      </p>
    )
  if (!metrics) return null
  const rate = (value: number) => `${(value / 1_000_000).toFixed(1)} Mbit/s`
  return (
    <p
      className="distribution-metrics flex flex-wrap gap-x-3 gap-y-2 text-xs text-muted-foreground"
      title={`MediaMTX observation: ${metrics.sampledAt}`}
    >
      <span>
        {metrics.state === "ready" ? "Source ready" : "Source not ready"}
      </span>
      <span>
        {metrics.readers} {metrics.readers === 1 ? "reader" : "readers"}
      </span>
      {metrics.inboundBitsPerSecond !== null &&
      metrics.outboundBitsPerSecond !== null ? (
        <>
          <span>Source: {rate(metrics.inboundBitsPerSecond)}</span>
          <span>To readers: {rate(metrics.outboundBitsPerSecond)}</span>
        </>
      ) : metrics.state === "ready" ? (
        <span>Measuring rates…</span>
      ) : null}
    </p>
  )
}
