import { listWorkers } from "@/lib/discovery";

export const runtime = "nodejs";

/** Worker pool snapshot for the UI. */
export async function GET() {
  const workers = await listWorkers();
  return Response.json({
    workers: workers.map((w) => ({
      id: w.id,
      name: w.name,
      machine: w.machine,
      models: w.models,
      accelerator: w.accelerator,
      engine: w.engine,
      load: Number.isFinite(w.load) ? w.load : null,
      rtt: Number.isFinite(w.rtt) ? Math.round(w.rtt) : null,
      reachable: w.reachable,
    })),
  });
}
