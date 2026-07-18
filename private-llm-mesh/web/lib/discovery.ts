import { z } from "zod";

import {
  APP_LABEL,
  getRstreamClient,
  LLM_ROLE,
  probeToken,
  ROLE_LABEL,
} from "./rstream";

export interface MeshWorker {
  name: string;
  id: string;
  host: string;
  machine: string;
  models: string[];
  accelerator: string;
  engine: string;
  ctx: number;
  load: number;
  rtt: number;
  reachable: boolean;
}

interface DiscoveryState {
  cache: { at: number; workers: MeshWorker[] } | null;
  inflight: Promise<MeshWorker[]> | null;
}

const CACHE_TTL_MS = 2000;
const state: DiscoveryState = {
  cache: null,
  inflight: null,
};

const healthSchema = z.object({ active: z.number() }).partial();
const modelsSchema = z
  .object({ data: z.array(z.object({ id: z.string() })) })
  .partial();

interface Probe {
  load: number;
  rtt: number;
  reachable: boolean;
  models: string[];
}

const DOWN: Probe = {
  load: Number.POSITIVE_INFINITY,
  rtt: Number.POSITIVE_INFINITY,
  reachable: false,
  models: [],
};

// Liveness, models, and load for one worker.
//
// `/v1/models` is the universal OpenAI endpoint — present on every variant — so
// it is the source of truth for liveness, round-trip time, and the model list.
// Reading the models from the worker itself (not a static label) means pulling a
// model in Ollama makes it appear, and a mislabelled worker can't advertise a
// model it does not serve.
//
// `/healthz` is our own worker's live in-flight count: a load enrichment, never a
// routing gate. A stock server lacks it, so load stays neutral and the worker
// still routes, ordered by RTT.
async function probeWorker(host: string): Promise<Probe> {
  const token = await probeToken();
  let models: string[] = [];
  let rtt: number;
  try {
    const url = new URL(`https://${host}/v1/models`);
    url.searchParams.set("rstream.token", token);
    const start = Date.now();
    const response = await fetch(url, { signal: AbortSignal.timeout(2000) });
    rtt = Date.now() - start;
    if (!response.ok) return DOWN;
    const parsed = modelsSchema.safeParse(
      await response.json().catch(() => ({})),
    );
    models =
      parsed.success && parsed.data.data
        ? parsed.data.data.map((m) => m.id)
        : [];
  } catch {
    return DOWN;
  }
  let load = 0;
  try {
    const url = new URL(`https://${host}/healthz`);
    url.searchParams.set("rstream.token", token);
    const response = await fetch(url, { signal: AbortSignal.timeout(2000) });
    if (response.ok) {
      const health = healthSchema.safeParse(
        await response.json().catch(() => ({})),
      );
      if (health.success && health.data.active !== undefined)
        load = health.data.active;
    }
  } catch {
    // No mesh /healthz — neutral load.
  }
  return { load, rtt, reachable: true, models };
}

/**
 * Fresh liveness probe used before committing a chat turn to a worker.
 */
export async function isWorkerAlive(host: string): Promise<boolean> {
  return (await probeWorker(host)).reachable;
}

async function refresh(): Promise<MeshWorker[]> {
  const tunnels = await getRstreamClient().tunnels.list({
    filters: { labels: { [ROLE_LABEL]: LLM_ROLE, app: APP_LABEL } },
  });
  const online = tunnels.filter(
    (tunnel): tunnel is typeof tunnel & { name: string } =>
      Boolean(tunnel.name) && tunnel.status === "online",
  );
  const workers = await Promise.all(
    online.map(async (tunnel) => {
      const labels = tunnel.labels ?? {};
      const host = tunnel.host ?? tunnel.hostname ?? "";
      const probe = host ? await probeWorker(host) : DOWN;
      const labelModels = (labels.models ?? "").split(",").filter(Boolean);
      return {
        name: tunnel.name,
        id: tunnel.id ?? "",
        host,
        machine: labels.host ?? "",
        // The worker's own /v1/models is authoritative; the label is a fallback
        // for a worker that is not answering probes yet.
        models: probe.models.length ? probe.models : labelModels,
        accelerator: labels.accelerator ?? "unknown",
        engine: labels.engine ?? "unknown",
        ctx: Number(labels.ctx ?? 0),
        load: probe.load,
        rtt: probe.rtt,
        reachable: probe.reachable,
      } satisfies MeshWorker;
    }),
  );
  state.cache = { at: Date.now(), workers };
  return workers;
}

/** Live worker pool, cached briefly. Falls back to the last snapshot on error. */
export async function listWorkers(): Promise<MeshWorker[]> {
  if (state.cache && Date.now() - state.cache.at < CACHE_TTL_MS) {
    return state.cache.workers;
  }
  state.inflight ??= refresh().finally(() => {
    state.inflight = null;
  });
  try {
    return await state.inflight;
  } catch {
    return state.cache?.workers ?? [];
  }
}

/** Union of models advertised across the pool, for the model picker. */
export async function listModels(): Promise<string[]> {
  const workers = await listWorkers();
  return [...new Set(workers.flatMap((w) => w.models))].sort();
}
