import { z } from "zod";

const healthSchema = z
  .object({
    active: z.number().nonnegative(),
    in_flight: z.number().nonnegative(),
    parallel: z.number().positive(),
    waiting: z.number().nonnegative(),
  })
  .partial();
const modelsSchema = z
  .object({ data: z.array(z.object({ id: z.string() })) })
  .partial();

export interface WorkerProbe {
  load: number;
  loadObservable: boolean;
  observedAt: number;
  parallel: number;
  waiting: number;
  rtt: number;
  reachable: boolean;
  models: string[];
}

export interface WorkerProbeDependencies {
  fetch: typeof fetch;
  now(): number;
}

export const DOWN_WORKER_PROBE: WorkerProbe = {
  load: Number.POSITIVE_INFINITY,
  loadObservable: false,
  observedAt: 0,
  parallel: 1,
  waiting: Number.POSITIVE_INFINITY,
  rtt: Number.POSITIVE_INFINITY,
  reachable: false,
  models: [],
};

async function probeModels(
  host: string,
  token: string,
  dependencies: WorkerProbeDependencies,
): Promise<{ models: string[]; reachable: boolean; rtt: number }> {
  const started = dependencies.now();
  try {
    const url = new URL(`https://${host}/v1/models`);
    url.searchParams.set("rstream.token", token);
    const response = await dependencies.fetch(url, {
      signal: AbortSignal.timeout(2000),
    });
    const rtt = dependencies.now() - started;
    if (!response.ok) return { models: [], reachable: false, rtt };
    const parsed = modelsSchema.safeParse(
      await response.json().catch(() => ({})),
    );
    return {
      models:
        parsed.success && parsed.data.data
          ? parsed.data.data.map((model) => model.id)
          : [],
      reachable: true,
      rtt,
    };
  } catch {
    return {
      models: [],
      reachable: false,
      rtt: dependencies.now() - started,
    };
  }
}

async function probeHealth(
  host: string,
  token: string,
  dependencies: WorkerProbeDependencies,
): Promise<z.infer<typeof healthSchema> | null> {
  try {
    const url = new URL(`https://${host}/healthz`);
    url.searchParams.set("rstream.token", token);
    const response = await dependencies.fetch(url, {
      signal: AbortSignal.timeout(2000),
    });
    if (!response.ok) return null;
    const health = healthSchema.safeParse(
      await response.json().catch(() => ({})),
    );
    return health.success ? health.data : null;
  } catch {
    return null;
  }
}

export async function probeWorkerTransport(
  host: string,
  token: string,
  dependencies: WorkerProbeDependencies,
): Promise<WorkerProbe> {
  const [models, health] = await Promise.all([
    probeModels(host, token, dependencies),
    probeHealth(host, token, dependencies),
  ]);
  if (!models.reachable) return DOWN_WORKER_PROBE;
  const loadObservable = Boolean(
    health && (health.in_flight !== undefined || health.active !== undefined),
  );
  return {
    load: health?.in_flight ?? health?.active ?? 0,
    loadObservable,
    observedAt: dependencies.now(),
    parallel: health?.parallel ?? 1,
    waiting: health?.waiting ?? 0,
    rtt: models.rtt,
    reachable: true,
    models: models.models,
  };
}

export async function probeWorkerLiveness(
  host: string,
  token: string,
  dependencies: WorkerProbeDependencies,
): Promise<boolean> {
  return (await probeModels(host, token, dependencies)).reachable;
}
