import { Cpu, Layers, Pin } from "lucide-react";

import { AddWorkerDialog } from "@/components/add-worker-dialog";
import type { PoolWorker } from "@/lib/use-pool";
import { cn } from "@/lib/utils";

function loadLabel(load: number | null, reachable: boolean): string {
  if (!reachable) return "unreachable";
  if (load === null) return "online";
  if (load === 0) return "idle";
  return `${load} active`;
}

export function WorkerPool({
  workers,
  pinnedId,
  onPin,
  loading,
  projectEndpoint,
  hideHeader = false,
}: {
  workers: PoolWorker[];
  pinnedId?: string | null;
  onPin?: (id: string) => void;
  loading: boolean;
  projectEndpoint: string;
  hideHeader?: boolean;
}) {
  return (
    <div className="flex flex-col gap-3">
      {hideHeader ? null : (
        <div className="flex items-center justify-between">
          <h2 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Worker pool
          </h2>
          <span className="text-xs text-muted-foreground">
            {workers.length} online
          </span>
        </div>
      )}

      <AddWorkerDialog projectEndpoint={projectEndpoint} />
      {workers.length === 0 ? (
        <p className="rounded-md border border-border bg-card px-3 py-3 text-sm text-muted-foreground">
          {loading
            ? "Discovering workers…"
            : "No workers yet. Add one to start serving models."}
        </p>
      ) : (
        <ul className="flex flex-col gap-2">
          {workers.map((worker) => {
            const pinned = worker.id === pinnedId;
            return (
              <li key={worker.id}>
                <button
                  type="button"
                  onClick={() => onPin?.(worker.id)}
                  aria-pressed={pinned}
                  title={
                    pinned
                      ? "Unpin — route across the pool"
                      : "Pin — route only to this worker"
                  }
                  className={cn(
                    "w-full rounded-md border bg-card px-3 py-2.5 text-left transition",
                    pinned
                      ? "border-foreground/40 ring-1 ring-foreground/20"
                      : "border-border hover:border-input",
                  )}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="flex min-w-0 items-center gap-1.5 truncate text-sm font-medium">
                      {pinned ? (
                        <Pin className="size-3 shrink-0 fill-current" />
                      ) : null}
                      {worker.name}
                      {worker.machine ? (
                        <span className="font-normal text-muted-foreground">
                          {worker.machine}
                        </span>
                      ) : null}
                    </span>
                    <span
                      className={cn(
                        "shrink-0 text-[11px] tabular-nums",
                        worker.reachable
                          ? "text-muted-foreground"
                          : "text-destructive",
                      )}
                    >
                      {loadLabel(worker.load, worker.reachable)}
                    </span>
                  </div>
                  <div className="mt-1.5 flex items-center gap-1 text-xs text-muted-foreground">
                    <Cpu className="size-3 shrink-0" />
                    <span className="truncate">
                      {worker.accelerator}
                      {worker.engine !== "unknown" ? ` · ${worker.engine}` : ""}
                      {worker.rtt !== null ? (
                        <span title="link round-trip latency, not generation speed">
                          {` · rtt ${worker.rtt} ms`}
                        </span>
                      ) : null}
                    </span>
                  </div>
                  {worker.models.length ? (
                    <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                      {worker.models.map((model) => (
                        <span
                          key={model}
                          className="inline-flex items-center gap-1"
                        >
                          <Layers className="size-3 shrink-0" />
                          {model}
                        </span>
                      ))}
                    </div>
                  ) : null}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
