export interface ServerConfig {
  labels: Record<string, string>;
  port: number;
  tunnel: {
    hostname?: string;
    name: string;
    reconnectInitialDelayMs: number;
    reconnectMaxDelayMs: number;
    rstreamAuth: boolean;
    tokenAuth: boolean;
  };
}

export function loadServerConfig(): ServerConfig {
  if (process.env.VERCEL) {
    throw new Error("Run the rstream tunnel server outside Vercel runtimes");
  }
  return {
    labels: parseLabels(process.env.RSTREAM_TUNNEL_LABELS),
    port: numberEnv("PORT", 3000, 1, 65535),
    tunnel: {
      hostname: optionalEnv("RSTREAM_TUNNEL_HOSTNAME"),
      name: nonEmptyEnv("RSTREAM_TUNNEL_NAME", "nextjs-preview"),
      reconnectInitialDelayMs: numberEnv(
        "RSTREAM_TUNNEL_RECONNECT_INITIAL_MS",
        1_000,
        1,
        60_000,
      ),
      reconnectMaxDelayMs: numberEnv(
        "RSTREAM_TUNNEL_RECONNECT_MAX_MS",
        30_000,
        1,
        300_000,
      ),
      rstreamAuth: booleanEnv("RSTREAM_TUNNEL_RSTREAM_AUTH"),
      tokenAuth: booleanEnv("RSTREAM_TUNNEL_TOKEN_AUTH"),
    },
  };
}

function parseLabels(value: string | undefined): Record<string, string> {
  const labels: Record<string, string> = {
    app: "nextjs-rstream-preview",
    env: "dev",
    framework: "nextjs",
    service: "nextjs",
  };
  for (const item of value?.split(",") ?? []) {
    const trimmed = item.trim();
    if (!trimmed) {
      continue;
    }
    const separator = trimmed.indexOf("=");
    if (separator <= 0) {
      throw new Error(`invalid RSTREAM_TUNNEL_LABELS item: ${trimmed}`);
    }
    const key = nonEmptyValue("label key", trimmed.slice(0, separator));
    const labelValue = nonEmptyValue(
      "label value",
      trimmed.slice(separator + 1),
    );
    labels[key] = labelValue;
  }
  return labels;
}

function optionalEnv(name: string): string | undefined {
  const value = process.env[name]?.trim();
  return value ? value : undefined;
}

function nonEmptyEnv(name: string, fallback: string): string {
  return nonEmptyValue(name, process.env[name] ?? fallback);
}

function nonEmptyValue(name: string, value: string): string {
  const trimmed = value.trim();
  if (!trimmed) {
    throw new Error(`${name} must not be empty`);
  }
  return trimmed;
}

function booleanEnv(name: string): boolean {
  const value = process.env[name]?.trim().toLowerCase();
  return value === "1" || value === "true" || value === "yes";
}

function numberEnv(
  name: string,
  fallback: number,
  min: number,
  max: number,
): number {
  const raw = process.env[name];
  const value = raw === undefined || raw.trim() === "" ? fallback : Number(raw);
  if (!Number.isInteger(value) || value < min || value > max) {
    throw new Error(`${name} must be an integer between ${min} and ${max}`);
  }
  return value;
}
