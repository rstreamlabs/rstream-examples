import type { MeshWorker } from "./discovery";

interface Reservation {
  createdAt: number;
  dispatchedAt: number | null;
}

const RESERVATION_TTL_MS = 10 * 60 * 1000;
const reservations = new Map<string, Map<symbol, Reservation>>();

export interface WorkerLease {
  worker: MeshWorker;
  markDispatched(): void;
  release(): void;
}

export interface WorkerScore {
  normalizedLoad: number;
  roundTripMilliseconds: number;
}

function activeReservations(workerId: string, now: number): Reservation[] {
  const current = reservations.get(workerId);
  if (!current) return [];
  for (const [id, reservation] of current) {
    if (now - reservation.createdAt >= RESERVATION_TTL_MS) current.delete(id);
  }
  if (current.size === 0) reservations.delete(workerId);
  return [...current.values()];
}

function normalizedLoad(worker: MeshWorker, now: number): number {
  const parallel = Math.max(1, worker.parallel);
  const local = activeReservations(worker.id, now);
  const pending = local.filter(
    (reservation) => reservation.dispatchedAt === null,
  ).length;
  const dispatched = local.length - pending;
  const observed = worker.loadObservable ? worker.load + worker.waiting : 0;
  return (pending + Math.max(observed, dispatched)) / parallel;
}

/** Lower load wins. RTT breaks only equal-load ties. */
export function score(worker: MeshWorker, now = Date.now()): WorkerScore {
  return {
    normalizedLoad: normalizedLoad(worker, now),
    roundTripMilliseconds: Math.max(0, worker.rtt),
  };
}

function compareScores(left: WorkerScore, right: WorkerScore): number {
  const loadDifference = left.normalizedLoad - right.normalizedLoad;
  if (Math.abs(loadDifference) >= Number.EPSILON) return loadDifference;
  return left.roundTripMilliseconds - right.roundTripMilliseconds;
}

export function eligibleWorkers(
  workers: MeshWorker[],
  model: string,
  exclude: Set<string>,
  now = Date.now(),
): MeshWorker[] {
  return workers
    .filter(
      (worker) =>
        worker.reachable &&
        worker.host &&
        worker.models.includes(model) &&
        !exclude.has(worker.id),
    )
    .sort((left, right) => compareScores(score(left, now), score(right, now)));
}

/**
 * Reserve the best eligible worker until the caller releases the lease. Local
 * reservations close the discovery-cache feedback gap and prevent concurrent
 * requests from stampeding the same worker. The random source only breaks
 * genuinely equal scores.
 */
export function reserveWorker(
  workers: MeshWorker[],
  model: string,
  exclude: Set<string>,
  rand: () => number = Math.random,
  now: () => number = Date.now,
): WorkerLease | null {
  const reservedAt = now();
  const candidates = eligibleWorkers(workers, model, exclude, reservedAt);
  if (candidates.length === 0) return null;
  const bestScore = score(candidates[0], reservedAt);
  const tied = candidates.filter(
    (worker) => compareScores(score(worker, reservedAt), bestScore) === 0,
  );
  const worker = tied[Math.floor(rand() * tied.length)] ?? candidates[0];
  const reservationId = Symbol(worker.id);
  const workerReservations = reservations.get(worker.id) ?? new Map();
  workerReservations.set(reservationId, {
    createdAt: reservedAt,
    dispatchedAt: null,
  });
  reservations.set(worker.id, workerReservations);
  let released = false;
  return {
    worker,
    markDispatched() {
      if (released) return;
      const reservation = reservations.get(worker.id)?.get(reservationId);
      if (reservation?.dispatchedAt === null) reservation.dispatchedAt = now();
    },
    release() {
      if (released) return;
      released = true;
      const current = reservations.get(worker.id);
      current?.delete(reservationId);
      if (current?.size === 0) reservations.delete(worker.id);
    },
  };
}

export function reservationCount(workerId: string, now = Date.now()): number {
  return activeReservations(workerId, now).length;
}

export function resetReservationsForTest(): void {
  reservations.clear();
}
