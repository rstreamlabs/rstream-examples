"use client"

import { apiErrorSchema } from "@/lib/validations/device"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { CopyPromptButton } from "@/components/copy-prompt-button"
import { CopyTextButton } from "@/components/copy-prompt-button"
import { createDeviceParamsSchema } from "@/lib/validations/device"
import { createDeviceResponseSchema } from "@/lib/validations/device"
import { DEVICE_LABEL } from "@/lib/rstream-labels"
import { Dialog } from "@/components/ui/dialog"
import { DialogClose } from "@/components/ui/dialog"
import { DialogContent } from "@/components/ui/dialog"
import { DialogDescription } from "@/components/ui/dialog"
import { DialogHeader } from "@/components/ui/dialog"
import { DialogTitle } from "@/components/ui/dialog"
import { DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Plus } from "lucide-react"
import { Skeleton } from "@/components/ui/skeleton"
import { Trash2 } from "lucide-react"
import { type DeviceView } from "@/lib/validations/device"
import { type FormEvent } from "react"
import { type ReactNode } from "react"
import { type Tunnel } from "@rstreamlabs/rstream/tunnel"
import { type UseRstreamOptions } from "@rstreamlabs/react"
import { type WatchPayload } from "@/lib/validations/device"
import { useEffect } from "react"
import { useMemo } from "react"
import { useRstream } from "@rstreamlabs/react"
import { useState } from "react"
import { VideoPlayer } from "@/components/video-player"
import { watchPayloadSchema } from "@/lib/validations/device"

type DeviceWithStatus = DeviceView & {
  online: boolean
}

type CreatedDevice = {
  device: DeviceView
  secret: string
}

const OFFLINE_GRACE_MS = 2500

export function DeviceDashboard({
  initialDevices,
}: {
  initialDevices: DeviceView[]
}) {
  const [devices, setDevices] = useState(initialDevices)
  const [manualActiveId, setManualActiveId] = useState<string | null>(null)
  const [watch, setWatch] = useState<WatchPayload | null>(null)
  const [error, setError] = useState<string | null>(null)
  const apiUrl = useAppOrigin()
  const watchOptions = useMemo(() => rstreamWatchOptions(watch), [watch])
  const rstream = useRstream(watchOptions)
  const liveOnlineIds = useMemo(
    () => onlineDeviceIds(rstream.tunnels),
    [rstream.tunnels],
  )
  const onlineIds = useStableOnlineIds(liveOnlineIds)
  const visibleDevices = useMemo(() => {
    const withStatus = devices.map((device) => ({
      ...device,
      online: onlineIds.has(device.id),
    }))
    return sortDevices(withStatus)
  }, [devices, onlineIds])
  const manualActive = manualActiveId
    ? visibleDevices.find((device) => device.id === manualActiveId)
    : null
  const active = manualActive ?? visibleDevices[0] ?? null
  useEffect(() => {
    void fetchWatch()
      .then((payload) => {
        setWatch(payload)
        setError(null)
      })
      .catch((err) => setError(errorMessage(err)))
  }, [])
  async function removeDevice(deviceId: string) {
    try {
      const response = await fetch(`/api/devices/${deviceId}`, {
        method: "DELETE",
      })
      const body = await responseJSON(response)
      if (!response.ok) {
        throw new Error(apiErrorSchema.parse(body).error)
      }
      const nextDevices = devices.filter((device) => device.id !== deviceId)
      setDevices(nextDevices)
      if (manualActiveId === deviceId) {
        setManualActiveId(null)
      }
      setError(null)
    } catch (err) {
      setError(errorMessage(err))
      throw err
    }
  }
  return (
    <div className="grid min-w-0 gap-5 lg:grid-cols-[360px_minmax(0,1fr)]">
      <section className="min-w-0 space-y-4">
        <DeviceDialog
          apiUrl={apiUrl}
          onCreated={(created) => {
            setDevices((current) => [created.device, ...current])
            setManualActiveId(created.device.id)
          }}
        />
        {error ? (
          <p className="text-sm font-semibold text-destructive">{error}</p>
        ) : null}
        {rstream.error ? (
          <p className="text-sm text-muted-foreground">
            rstream watch: {rstream.error.message}
          </p>
        ) : null}
        <div className="min-w-0 space-y-2">
          {visibleDevices.length > 0 ? (
            visibleDevices.map((device) => (
              <DeviceRow
                key={device.id}
                active={active?.id === device.id}
                device={device}
                onRemove={() => removeDevice(device.id)}
                onSelect={() => setManualActiveId(device.id)}
              />
            ))
          ) : (
            <DeviceListSkeleton />
          )}
        </div>
      </section>
      <section className="min-w-0 overflow-hidden rounded-lg border border-border bg-card p-4 sm:p-5">
        <div className="min-w-0 space-y-5">
          <SelectedDeviceHeader device={active} />
          {active?.online ? (
            <VideoPlayer deviceId={active.id} />
          ) : (
            <EmptyState
              copy={
                active
                  ? "Run the producer command for this device."
                  : "Add a device to get started."
              }
              action={
                active ? (
                  <CopyPromptButton
                    prompt={producerSetupPrompt({ device: active, apiUrl })}
                  />
                ) : undefined
              }
            />
          )}
        </div>
      </section>
    </div>
  )
}

function SelectedDeviceHeader({ device }: { device: DeviceWithStatus | null }) {
  return (
    <div className="flex min-h-[56px] min-w-0 flex-wrap items-center justify-between gap-3">
      {device ? (
        <>
          <div className="min-w-0">
            <p className="text-sm text-muted-foreground">Selected device</p>
            <h2 className="break-words text-2xl font-semibold text-foreground">
              {device.name}
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">
              {presenceLabel(device)}
            </p>
          </div>
          <Badge
            className="shrink-0"
            tone={device.online ? "online" : "offline"}
          >
            {device.online ? "Online" : "Offline"}
          </Badge>
        </>
      ) : (
        <>
          <div className="space-y-2">
            <Skeleton className="h-4 w-28" />
            <Skeleton className="h-7 w-44" />
          </div>
          <Skeleton className="h-8 w-16" />
        </>
      )}
    </div>
  )
}

function DeviceDialog({
  apiUrl,
  onCreated,
}: {
  apiUrl: string
  onCreated: (created: CreatedDevice) => void
}) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [created, setCreated] = useState<CreatedDevice | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const command = created ? producerCommand(apiUrl, created.secret) : null
  async function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) {
      return
    }
    const parsed = createDeviceParamsSchema.safeParse({ name })
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Invalid device name.")
      return
    }
    setPending(true)
    try {
      const response = await fetch("/api/devices", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(parsed.data),
      })
      const body = await responseJSON(response)
      if (!response.ok) {
        throw new Error(apiErrorSchema.parse(body).error)
      }
      const next = createDeviceResponseSchema.parse(body)
      setCreated(next)
      setError(null)
      onCreated(next)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setPending(false)
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (nextOpen) {
          return
        }
        setName("")
        setCreated(null)
        setError(null)
      }}
    >
      <DialogTrigger asChild>
        <Button className="w-full">
          <Plus className="h-4 w-4" />
          Add device
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[calc(100dvh-2rem)] max-w-[calc(100vw-2rem)] overflow-y-auto p-4 sm:max-w-2xl sm:p-5">
        <DialogHeader>
          <DialogTitle>Add device</DialogTitle>
          <DialogDescription>
            Create a device, then run the producer with the generated secret.
          </DialogDescription>
        </DialogHeader>
        {created && command ? (
          <div className="min-w-0 space-y-4">
            <div>
              <p className="text-sm font-medium text-foreground">
                {created.device.name}
              </p>
              <p className="mt-1 text-sm leading-6 text-muted-foreground">
                Copy it now and set it on the video producer as DEVICE_SECRET.
              </p>
            </div>
            <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-all rounded-md border border-border bg-background p-3 text-xs leading-6 text-foreground sm:text-sm">
              <code>{created.secret}</code>
            </pre>
            <div>
              <p className="text-sm font-medium text-foreground">
                Producer command
              </p>
              <p className="mt-1 text-sm leading-6 text-muted-foreground">
                Run these commands on the video producer.
              </p>
              <pre className="mt-2 max-w-full overflow-x-auto whitespace-pre-wrap break-words rounded-md border border-border bg-background p-3 text-xs leading-6 text-foreground sm:text-sm">
                <code>{command}</code>
              </pre>
            </div>
            <div className="flex flex-col gap-2 sm:flex-row sm:justify-end">
              <CopyTextButton
                className="w-full sm:w-auto"
                label="Copy instructions"
                text={command}
              />
              <CopyPromptButton
                className="w-full sm:w-auto"
                label="Copy prompt"
                prompt={producerSetupPrompt({
                  device: created.device,
                  apiUrl,
                  secret: created.secret,
                })}
              />
              <DialogClose asChild>
                <Button
                  type="button"
                  variant="outline"
                  className="w-full sm:w-auto"
                >
                  Close
                </Button>
              </DialogClose>
            </div>
          </div>
        ) : (
          <form className="min-w-0 space-y-4" onSubmit={onCreate}>
            <Input
              className="min-w-0"
              value={name}
              onChange={(event) => {
                setName(event.target.value)
                setError(null)
              }}
              placeholder="Warehouse camera"
              aria-label="Device name"
              aria-invalid={Boolean(error)}
              aria-describedby={error ? "device-name-error" : undefined}
            />
            {error ? (
              <p id="device-name-error" className="text-sm text-destructive">
                {error}
              </p>
            ) : null}
            <Button
              className="w-full sm:w-auto"
              disabled={pending}
              type="submit"
            >
              {pending ? "Creating..." : "Create device"}
            </Button>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

function DeviceRow({
  active,
  device,
  onRemove,
  onSelect,
}: {
  active: boolean
  device: DeviceWithStatus
  onRemove: () => Promise<void>
  onSelect: () => void
}) {
  return (
    <div
      onClick={onSelect}
      className={[
        "min-w-0 cursor-pointer rounded-lg border bg-card transition-colors",
        active
          ? "border-foreground"
          : "border-border hover:border-foreground/30",
      ].join(" ")}
    >
      <Button
        type="button"
        variant="ghost"
        className="h-auto min-w-0 w-full justify-between rounded-b-none rounded-t-lg px-4 py-4 text-left hover:bg-transparent"
        onClick={(event) => {
          event.stopPropagation()
          onSelect()
        }}
      >
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium text-foreground">{device.name}</p>
          <p className="mt-1 truncate text-xs text-muted-foreground">
            {device.tunnelName}
          </p>
          <p className="mt-1 truncate text-xs text-muted-foreground">
            {presenceLabel(device)}
          </p>
        </div>
        <Badge className="shrink-0" tone={device.online ? "online" : "offline"}>
          {device.online ? "Online" : "Offline"}
        </Badge>
      </Button>
      <div className="flex items-center justify-end border-t border-border px-3 py-2">
        <DeleteDeviceDialog device={device} onConfirm={onRemove} />
      </div>
    </div>
  )
}

function DeviceListSkeleton() {
  return (
    <div className="rounded-lg border border-border bg-card">
      <div className="flex items-center justify-between gap-3 p-4">
        <div className="min-w-0 space-y-2">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-3 w-40 sm:w-56" />
        </div>
        <Skeleton className="h-8 w-16" />
      </div>
      <div className="flex items-center justify-end border-t border-border px-3 py-2">
        <Skeleton className="h-9 w-9" />
      </div>
    </div>
  )
}

function DeleteDeviceDialog({
  device,
  onConfirm,
}: {
  device: DeviceWithStatus
  onConfirm: () => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function deleteDevice() {
    if (pending) {
      return
    }
    setPending(true)
    try {
      await onConfirm()
      setOpen(false)
      setError(null)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setPending(false)
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (!nextOpen) {
          setError(null)
        }
      }}
    >
      <DialogTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={(event) => {
            event.stopPropagation()
          }}
        >
          <Trash2 className="h-4 w-4" />
          <span className="sr-only">Delete {device.name}</span>
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[calc(100dvh-2rem)] max-w-[calc(100vw-2rem)] overflow-y-auto p-4 sm:max-w-md sm:p-5">
        <DialogHeader>
          <DialogTitle>Delete device</DialogTitle>
          <DialogDescription>
            Delete {device.name}? A running producer using this secret will no
            longer be authorized by this app.
          </DialogDescription>
        </DialogHeader>
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button
            type="button"
            variant="outline"
            className="w-full sm:w-auto"
            onClick={() => setOpen(false)}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            className="w-full sm:w-auto"
            disabled={pending}
            onClick={deleteDevice}
          >
            {pending ? "Deleting..." : "Delete device"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function EmptyState({ action, copy }: { action?: ReactNode; copy: string }) {
  return (
    <div className="flex aspect-video flex-col items-center justify-center gap-4 rounded-lg border border-dashed border-border bg-background px-6 text-center">
      <p className="max-w-sm text-sm leading-6 text-muted-foreground">{copy}</p>
      {action}
    </div>
  )
}

function useAppOrigin() {
  const [origin, setOrigin] = useState("http://localhost:3000")
  useEffect(() => {
    setOrigin(window.location.origin)
  }, [])
  return origin
}

function rstreamWatchOptions(
  watch: WatchPayload | null,
): UseRstreamOptions | undefined {
  return watch
    ? {
        auth: async () => (await fetchWatch()).auth.token,
        engine: watch.engine,
        transport: "websocket",
      }
    : undefined
}

function useStableOnlineIds(liveOnlineIds: Set<string>) {
  const [onlineIds, setOnlineIds] = useState(() => new Set(liveOnlineIds))
  useEffect(() => {
    setOnlineIds((current) => mergeSets(current, liveOnlineIds))
    const timeout = window.setTimeout(() => {
      setOnlineIds((current) =>
        sameSet(current, liveOnlineIds) ? current : new Set(liveOnlineIds),
      )
    }, OFFLINE_GRACE_MS)
    return () => window.clearTimeout(timeout)
  }, [liveOnlineIds])
  return onlineIds
}

function onlineDeviceIds(tunnels: Tunnel[]) {
  return new Set(
    tunnels.flatMap((tunnel) => {
      const deviceId = tunnel.labels?.[DEVICE_LABEL]
      return tunnel.status === "online" && deviceId ? [deviceId] : []
    }),
  )
}

function mergeSets(left: Set<string>, right: Set<string>) {
  const next = new Set(left)
  for (const value of right) {
    next.add(value)
  }
  return sameSet(left, next) ? left : next
}

function sameSet(left: Set<string>, right: Set<string>) {
  if (left.size !== right.size) {
    return false
  }
  for (const value of left) {
    if (!right.has(value)) {
      return false
    }
  }
  return true
}

function sortDevices(devices: DeviceWithStatus[]) {
  return [...devices].sort((left, right) => {
    if (left.online !== right.online) {
      return left.online ? -1 : 1
    }
    return left.name.localeCompare(right.name)
  })
}

function presenceLabel(device: DeviceWithStatus) {
  if (device.online) {
    return device.onlineSince
      ? `Online since: ${formatUTCDateTime(device.onlineSince)}`
      : "Online"
  }
  if (device.lastSeenAt) {
    return `Last seen at: ${formatUTCDateTime(device.lastSeenAt)}`
  }
  return "Last seen at: never"
}

function formatUTCDateTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return "unknown"
  }
  const year = date.getUTCFullYear()
  const month = padDatePart(date.getUTCMonth() + 1)
  const day = padDatePart(date.getUTCDate())
  const hour = padDatePart(date.getUTCHours())
  const minute = padDatePart(date.getUTCMinutes())
  return `${year}-${month}-${day} ${hour}:${minute} UTC`
}

function padDatePart(value: number) {
  return value.toString().padStart(2, "0")
}

function producerCommand(apiUrl: string, secret: string) {
  return [
    "git clone https://github.com/rstreamlabs/rstream-examples.git",
    "cd rstream-examples/webrtc-video-streaming",
    "make build-provisioning",
    `export API_URL=${shellQuote(apiUrl)}`,
    `export DEVICE_SECRET=${shellQuote(secret)}`,
    "./webrtc-video-streaming -config ./config.provisioning.h264.yaml",
  ].join("\n")
}

function producerSetupPrompt({
  apiUrl,
  device,
  secret,
}: {
  apiUrl: string
  device: DeviceView
  secret?: string
}) {
  return [
    "Help me run the rstream WebRTC video producer for this device.",
    "",
    "Repository: https://github.com/rstreamlabs/rstream-examples",
    "README: https://github.com/rstreamlabs/rstream-examples/tree/main/webrtc-video-streaming#readme",
    "Sample directory: webrtc-video-streaming",
    `Platform API URL: ${apiUrl}`,
    `Device name: ${device.name}`,
    `Tunnel name: ${device.tunnelName}`,
    secret
      ? `Device secret: ${secret}`
      : `Device secret: use the secret shown when this device was created. It starts with ${device.secretPrefix}. If it was lost, delete and recreate the device because the app stores only its hash.`,
    "",
    "On the device:",
    "1. Clone or update the repository.",
    "2. Install Go, a C compiler, pkg-config, and GStreamer development packages as described in the README. Node.js/npm are not required for this provisioning build.",
    "3. Run these commands from rstream-examples/webrtc-video-streaming:",
    "make build-provisioning",
    `export API_URL=${shellQuote(apiUrl)}`,
    secret
      ? `export DEVICE_SECRET=${shellQuote(secret)}`
      : 'export DEVICE_SECRET="<device-secret>"',
    "./webrtc-video-streaming -config ./config.provisioning.h264.yaml",
    "",
    "Do not install or configure the rstream CLI on the device for this platform mode. The producer must request tunnel and TURN provisioning from the platform API.",
  ].join("\n")
}

function shellQuote(value: string) {
  return `'${value.replaceAll("'", "'\"'\"'")}'`
}

async function fetchWatch() {
  const response = await fetch("/api/rstream/watch")
  const body = await responseJSON(response)
  if (!response.ok) {
    throw new Error(apiErrorSchema.parse(body).error)
  }
  return watchPayloadSchema.parse(body)
}

async function responseJSON(response: Response): Promise<unknown> {
  return response.json()
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : "Request failed"
}
