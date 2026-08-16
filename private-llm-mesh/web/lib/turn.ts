import "server-only";

import { isWorkerAlive, listWorkers } from "./discovery";
import { mintConnectToken } from "./rstream";
import { selectTurn, type Turn } from "./turn-selection";

export type { Turn } from "./turn-selection";

/** Reserve a live worker and mint its single-path, short-lived token. */
export function mintTurn(
  model: string,
  options: { workerId?: string; maxAttempts?: number } = {},
): Promise<Turn | null> {
  return selectTurn(model, options, {
    listWorkers,
    isWorkerAlive,
    mintConnectToken,
  });
}
