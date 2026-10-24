"use client";

import { useRstream, type UseRstreamOptions } from "@rstreamlabs/react/hooks";
import { useEffect, useMemo, useState } from "react";
import { z } from "zod";
import { livePoolWorkers, type PoolWorker } from "./pool-workers";

export type { PoolWorker } from "./pool-workers";

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
    return livePoolWorkers(rstream.tunnels, probed);
  }, [rstream.tunnels, rstream.state, probed]);
  const models = useMemo(
    () => [...new Set(workers.flatMap((w) => w.models))].sort(),
    [workers],
  );
  const loading = rstream.state === "connecting" && workers.length === 0;
  return { workers, models, loading };
}
