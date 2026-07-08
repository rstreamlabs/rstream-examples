import type { MeshWorker } from "./discovery";

/**
 * Lower is better: the worker's in-flight load plus a sub-1 round-trip tiebreak
 * (RTT in milliseconds, capped at one second). Two workers at equal load are
 * separated by latency; latency never outweighs a full unit of load.
 */
export function score(worker: Pick<MeshWorker, "load" | "rtt">): number {
  return worker.load + Math.min(worker.rtt, 1000) / 1000;
}

/**
 * Workers that can serve `model` right now, best-scored first. A worker is
 * eligible when it is reachable, has a public host, advertises the model, and
 * is not in the exclusion set. A worker may advertise several models, so the
 * same worker can be eligible for different model requests.
 */
export function eligibleWorkers(
  workers: MeshWorker[],
  model: string,
  exclude: Set<string>,
): MeshWorker[] {
  return workers
    .filter(
      (w) =>
        w.reachable && w.host && w.models.includes(model) && !exclude.has(w.id),
    )
    .sort((a, b) => score(a) - score(b));
}

/**
 * Pick a worker for `model`, load-balancing across the two least-loaded
 * candidates so identical replicas share traffic instead of stampeding one
 * worker. Returns null when nothing can serve the model. `rand` is injectable
 * for deterministic tests and defaults to Math.random in production.
 */
export function pickWorker(
  workers: MeshWorker[],
  model: string,
  exclude: Set<string>,
  rand: () => number = Math.random,
): MeshWorker | null {
  const candidates = eligibleWorkers(workers, model, exclude);
  if (candidates.length === 0) return null;
  const top = candidates.slice(0, 2);
  return top[Math.floor(rand() * top.length)] ?? null;
}
