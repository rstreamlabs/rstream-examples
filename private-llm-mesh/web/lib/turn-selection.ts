import type { MeshWorker } from "./discovery";
import { reserveWorker, type WorkerLease } from "./routing.ts";

const CHAT_PATH = "^/v1/chat/completions$";

export interface Turn {
  url: string;
  token: string;
  model: string;
  worker: string;
  ctx: number;
  markDispatched(): void;
  release(): void;
}

export interface TurnDependencies {
  listWorkers(): Promise<MeshWorker[]>;
  isWorkerAlive(host: string): Promise<boolean>;
  mintConnectToken(tunnelId: string, pathRegex: string): Promise<string>;
}

async function mintFor(
  lease: WorkerLease,
  model: string,
  dependencies: TurnDependencies,
): Promise<Turn> {
  try {
    const token = await dependencies.mintConnectToken(
      lease.worker.id,
      CHAT_PATH,
    );
    return {
      url: `https://${lease.worker.host}`,
      token,
      model,
      worker: lease.worker.name,
      ctx: lease.worker.ctx || 4096,
      markDispatched: lease.markDispatched,
      release: lease.release,
    };
  } catch (error) {
    lease.release();
    throw error;
  }
}

async function mintAuto(
  model: string,
  remaining: number,
  excluded: Set<string>,
  dependencies: TurnDependencies,
): Promise<Turn | null> {
  if (remaining <= 0) return null;
  const lease = reserveWorker(
    await dependencies.listWorkers(),
    model,
    excluded,
  );
  if (!lease) return null;
  let alive: boolean;
  try {
    alive = await dependencies.isWorkerAlive(lease.worker.host);
  } catch (error) {
    lease.release();
    throw error;
  }
  if (!alive) {
    excluded.add(lease.worker.id);
    lease.release();
    return mintAuto(model, remaining - 1, excluded, dependencies);
  }
  return mintFor(lease, model, dependencies);
}

async function mintPinned(
  model: string,
  workerId: string,
  dependencies: TurnDependencies,
): Promise<Turn | null> {
  const workers = await dependencies.listWorkers();
  const worker = workers.find(
    (candidate) =>
      candidate.id === workerId &&
      candidate.reachable &&
      candidate.host &&
      candidate.models.includes(model),
  );
  if (!worker || !(await dependencies.isWorkerAlive(worker.host))) return null;
  const lease = reserveWorker([worker], model, new Set());
  return lease ? mintFor(lease, model, dependencies) : null;
}

/**
 * Reserve a worker and mint a scoped token. Auto-routing retries only before
 * the model request starts. Once streaming or tools begin, callers must never
 * replay the turn automatically because that could duplicate side effects.
 */
export async function selectTurn(
  model: string,
  options: { workerId?: string; maxAttempts?: number },
  dependencies: TurnDependencies,
): Promise<Turn | null> {
  const { workerId, maxAttempts = 3 } = options;
  if (workerId) return mintPinned(model, workerId, dependencies);
  return mintAuto(model, maxAttempts, new Set<string>(), dependencies);
}
