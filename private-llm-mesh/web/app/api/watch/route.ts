import { NextResponse } from "next/server";

import { getServerUser } from "@/lib/auth";
import { getEngine, watchToken } from "@/lib/rstream";

export const runtime = "nodejs";

/** Watch credentials for browser pool presence. */
export async function GET(): Promise<Response> {
  const user = await getServerUser();
  if (!user) {
    return NextResponse.json({ error: "unauthorized" }, { status: 401 });
  }
  const [token, engine] = await Promise.all([watchToken(), getEngine()]);
  return NextResponse.json({ auth: { token }, engine });
}
