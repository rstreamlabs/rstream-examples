import "server-only";

import { tavily } from "@tavily/core";
import { tool } from "ai";
import { z } from "zod";

const client = tavily({ apiKey: process.env.TAVILY_API_KEY });

function webError(err: unknown): { error: string } {
  const message = err instanceof Error ? err.message : "web request failed";
  return {
    error: /keyless|limit/i.test(message)
      ? "Web access is rate-limited — set TAVILY_API_KEY for full access."
      : message,
  };
}

/** Web search and fetch tools with model-sized output budgets. */
export function webTools(maxChars: number) {
  const snippetCap = Math.max(200, Math.floor(maxChars / 8));
  const pageCap = Math.max(1500, maxChars);
  return {
    web_search: tool({
      description:
        "Search the web for current or unknown information. Returns a short answer " +
        "and the top results (title, url, snippet). Use it whenever the answer may " +
        "have changed or you are unsure — do not guess.",
      inputSchema: z.object({
        query: z.string().describe("the search query"),
      }),
      execute: async ({ query }) => {
        try {
          const result = await client.search(query, {
            maxResults: 4,
            includeAnswer: true,
          });
          return {
            answer: result.answer,
            results: result.results.slice(0, 4).map((r) => ({
              title: r.title,
              url: r.url,
              snippet: r.content.slice(0, snippetCap),
            })),
          };
        } catch (err) {
          return webError(err);
        }
      },
    }),
    web_fetch: tool({
      description:
        "Fetch and read the main text content of a web page by its URL.",
      inputSchema: z.object({
        url: z.string().url().describe("the page URL"),
      }),
      execute: async ({ url }) => {
        try {
          const result = await client.extract([url]);
          const page = result.results[0];
          return page
            ? { url: page.url, content: page.rawContent.slice(0, pageCap) }
            : { url, error: "could not extract this page" };
        } catch (err) {
          return webError(err);
        }
      },
    }),
  };
}
