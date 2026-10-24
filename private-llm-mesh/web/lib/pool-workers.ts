import type { ListedTunnel } from "@rstreamlabs/tunnels";

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

export function livePoolWorkers(
  tunnels: readonly ListedTunnel[],
  probed: readonly PoolWorker[],
): PoolWorker[] {
  const live = tunnels.filter(
    (tunnel): tunnel is ListedTunnel & { id: string; name: string } =>
      tunnel.status === "online" && Boolean(tunnel.id) && Boolean(tunnel.name),
  );
  const metrics = new Map(probed.map((worker) => [worker.id, worker]));
  return live.map((tunnel) => {
    const labels = tunnel.labels ?? {};
    const worker = metrics.get(tunnel.id);
    return {
      id: tunnel.id,
      name: tunnel.name,
      machine: labels.host ?? "",
      // Probed models take precedence over labels until the next worker probe.
      models:
        worker?.models ?? (labels.models ?? "").split(",").filter(Boolean),
      accelerator: labels.accelerator ?? "unknown",
      engine: labels.engine ?? "unknown",
      load: worker?.load ?? null,
      rtt: worker?.rtt ?? null,
      reachable: worker?.reachable ?? true,
    };
  });
}
