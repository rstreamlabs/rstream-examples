import { NextResponse } from "next/server";

import { getServerUser } from "@/lib/auth";
import { listWorkers } from "@/lib/discovery";

export const runtime = "nodejs";

/** Worker pool snapshot for the UI. */
export async function GET(): Promise<Response> {
  const user = await getServerUser();
  if (!user) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
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
