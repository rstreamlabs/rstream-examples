import "server-only";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type {
  CallToolResult,
  CompatibilityCallToolResult,
  Tool,
} from "@modelcontextprotocol/sdk/types.js";

import { mcpEndpoint, mintMcpToken } from "./rstream";

export type McpTool = Tool;
type McpCallResult = CallToolResult | CompatibilityCallToolResult;

function isCallToolResult(result: McpCallResult): result is CallToolResult {
  return "content" in result;
}

async function withMcpClient<T>(
  run: (client: Client) => Promise<T>,
): Promise<T> {
  const transport = new StreamableHTTPClientTransport(new URL(mcpEndpoint()), {
    requestInit: {
      headers: { authorization: `Bearer ${await mintMcpToken()}` },
    },
  });
  const client = new Client({ name: "private-llm-mesh", version: "1.0.0" });
  await client.connect(transport);
  try {
    return await run(client);
  } finally {
    await client.close();
  }
}

/**
 * Discover the MCP tools and the server's own `instructions` in one connection.
 * The MCP protocol has the server describe how its tools should be used; the
 * client propagates that to the model rather than hardcoding it app-side.
 */
export async function discoverMcp(): Promise<{
  tools: McpTool[];
  instructions?: string;
}> {
  return withMcpClient(async (client) => ({
    tools: (await client.listTools()).tools,
    instructions: client.getInstructions(),
  }));
}

/** Call one MCP tool and normalize its output for the model. */
export async function callMcpTool(
  name: string,
  args: Record<string, unknown>,
): Promise<unknown> {
  const result: McpCallResult = await withMcpClient((client) =>
    client.callTool({ name, arguments: args }),
  );
  if (!isCallToolResult(result)) return result.toolResult;
  if (result.structuredContent !== undefined) return result.structuredContent;
  return (result.content ?? [])
    .map((part) => (part.type === "text" ? part.text : ""))
    .filter(Boolean)
    .join("\n");
}
