import "server-only";

import { jsonSchema, tool, type Tool } from "ai";

import { callMcpTool, discoverMcp, type McpTool } from "./mcp-client";

interface McpDiscovery {
  tools: McpTool[];
  instructions?: string;
}

interface McpToolsState {
  cache: { at: number; discovery: McpDiscovery } | null;
}

function exposeToAgent(name: string): boolean {
  return name.startsWith("rstream_webtty_");
}

const CACHE_TTL_MS = 60_000;
const state: McpToolsState = {
  cache: null,
};

async function discover(): Promise<McpDiscovery> {
  if (state.cache && Date.now() - state.cache.at < CACHE_TTL_MS) {
    return state.cache.discovery;
  }
  const { tools, instructions } = await discoverMcp();
  const discovery = {
    tools: tools.filter((t) => exposeToAgent(t.name)),
    instructions,
  };
  state.cache = { at: Date.now(), discovery };
  return discovery;
}

async function discoverOrNone(): Promise<McpDiscovery> {
  try {
    return await discover();
  } catch {
    return { tools: [] };
  }
}

/** The MCP server's own usage instructions, propagated into the model context. */
export async function mcpInstructions(): Promise<string | undefined> {
  return (await discoverOrNone()).instructions;
}

/** Live webtty MCP tools exposed to the agent. */
export async function mcpTools(): Promise<Record<string, Tool>> {
  const { tools: discovered } = await discoverOrNone();
  const tools: Record<string, Tool> = {};
  for (const mcpTool of discovered) {
    tools[mcpTool.name] = tool({
      description: mcpTool.description,
      inputSchema: jsonSchema<Record<string, unknown>>(
        mcpTool.inputSchema ?? { type: "object", properties: {} },
      ),
      needsApproval: mcpTool.annotations?.destructiveHint ?? false,
      execute: (args) => callMcpTool(mcpTool.name, args),
    });
  }
  return tools;
}
