"use client";

import { useRstream, type UseRstreamOptions } from "@rstreamlabs/react";
import { useEffect, useMemo, useState } from "react";
import { z } from "zod";

export interface PoolWorker {
  id: string;
  name: string;
  machine: string;
  models: string[];
  accelerator: string;
  engine: string;
  load: number | null;
  rtt: number | null;
  reachable: boolean;
}

interface PoolState {
  workers: PoolWorker[];
  models: string[];
  loading: boolean;
}

const watchPayloadSchema = z.object({
  auth: z.object({ token: z.string() }),
  engine: z.string(),
});
type WatchPayload = z.infer<typeof watchPayloadSchema>;

const poolSchema = z.object({
  workers: z.array(
    z.object({
      id: z.string(),
      name: z.string(),
      machine: z.string(),
      models: z.array(z.string()),
      accelerator: z.string(),
      engine: z.string(),
      load: z.number().nullable(),
      rtt: z.number().nullable(),
      reachable: z.boolean(),
    }),
  ),
});

async function fetchWatch(): Promise<WatchPayload> {
  const response = await fetch("/api/watch", { cache: "no-store" });
  if (!response.ok) throw new Error("watch unavailable");
  return watchPayloadSchema.parse(await response.json());
}

/** Live worker pool from browser Watch plus server-side metrics. */
export function usePool(): PoolState {
  const [watch, setWatch] = useState<WatchPayload | null>(null);
  const [probed, setProbed] = useState<PoolWorker[]>([]);
  useEffect(() => {
    void fetchWatch()
      .then(setWatch)
      .catch(() => undefined);
  }, []);

  const options: UseRstreamOptions | undefined = useMemo(
    () =>
      watch
        ? {
            auth: async () => (await fetchWatch()).auth.token,
            engine: watch.engine,
            transport: "websocket",
          }
        : undefined,
    [watch],
  );
  const rstream = useRstream(options);
  useEffect(() => {
    const controller = new AbortController();
    const tick = async () => {
      try {
        const response = await fetch("/api/pool", {
          cache: "no-store",
          signal: controller.signal,
        });
        if (!response.ok) return;
        setProbed(poolSchema.parse(await response.json()).workers);
      } catch {
        return;
      }
    };
    void tick();
    const timer = setInterval(() => void tick(), 5000);
    return () => {
      controller.abort();
      clearInterval(timer);
    };
  }, []);
  const workers = useMemo<PoolWorker[]>(() => {
    if (rstream.state !== "connected") return probed;
    const live = rstream.tunnels.filter(
      (tunnel): tunnel is typeof tunnel & { name: string } =>
        tunnel.status === "online" && Boolean(tunnel.name),
    );
    const metrics = new Map(probed.map((w) => [w.id, w]));
    return live.map((tunnel) => {
      const labels = tunnel.labels ?? {};
      const m = metrics.get(tunnel.id);
      return {
        id: tunnel.id,
        name: tunnel.name,
        machine: labels.host ?? "",
        // Prefer the models probed from the worker's own /v1/models; the label is
        // only a fallback until the first probe lands.
        models: m?.models ?? (labels.models ?? "").split(",").filter(Boolean),
        accelerator: labels.accelerator ?? "unknown",
        engine: labels.engine ?? "unknown",
        load: m?.load ?? null,
        rtt: m?.rtt ?? null,
        reachable: true,
      };
    });
  }, [rstream.tunnels, rstream.state, probed]);
  const models = useMemo(
    () => [...new Set(workers.flatMap((w) => w.models))].sort(),
    [workers],
  );
  const loading = rstream.state === "connecting" && workers.length === 0;
  return { workers, models, loading };
}
