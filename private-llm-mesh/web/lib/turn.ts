import "server-only";

import { isWorkerAlive, listWorkers, type MeshWorker } from "./discovery";
import { pickWorker } from "./routing";
import { mintConnectToken } from "./rstream";

const CHAT_PATH = "^/v1/chat/completions$";

export interface Turn {
  url: string;
  token: string;
  model: string;
  worker: string;
  ctx: number;
}

async function mintFor(worker: MeshWorker, model: string): Promise<Turn> {
  const token = await mintConnectToken(worker.id, CHAT_PATH);
  return {
    url: `https://${worker.host}`,
    token,
    model,
    worker: worker.name,
    ctx: worker.ctx || 4096,
  };
}

async function mintAuto(
  model: string,
  remaining: number,
  excluded: Set<string>,
): Promise<Turn | null> {
  if (remaining <= 0) return null;
  const workers = await listWorkers();
  const worker = pickWorker(workers, model, excluded);
  if (!worker) return null;
  if (!(await isWorkerAlive(worker.host))) {
    excluded.add(worker.id);
    return mintAuto(model, remaining - 1, excluded);
  }
  return mintFor(worker, model);
}

// A pinned worker is honoured exactly: it must be reachable, serve the model, and
// answer a fresh liveness probe — otherwise the turn fails rather than silently
// routing elsewhere.
async function mintPinned(
  model: string,
  workerId: string,
): Promise<Turn | null> {
  const workers = await listWorkers();
  const worker = workers.find(
    (w) =>
      w.id === workerId && w.reachable && w.host && w.models.includes(model),
  );
  if (!worker || !(await isWorkerAlive(worker.host))) return null;
  return mintFor(worker, model);
}

/**
 * Mint a scoped chat token for a model. Auto-routes to the least-loaded worker,
 * or honours an explicit `workerId` pin (no failover — the pin is respected).
 */
export async function mintTurn(
  model: string,
  options: { workerId?: string; maxAttempts?: number } = {},
): Promise<Turn | null> {
  const { workerId, maxAttempts = 3 } = options;
  if (workerId) return mintPinned(model, workerId);
  return mintAuto(model, maxAttempts, new Set<string>());
}
