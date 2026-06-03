import { RstreamLogo } from "@/components/rstream-logo";
import { headers } from "next/headers";
import { readRecentGitHubWebhookEvents } from "@/lib/webhooks";

const GUIDE_URL =
  "https://rstream.io/guides/expose-a-local-nextjs-app-with-rstream";
const GITHUB_URL =
  "https://github.com/rstreamlabs/rstream-examples/tree/main/nextjs-rstream-preview";

export default async function Home() {
  const origin = await requestOrigin();
  const events = await readRecentGitHubWebhookEvents();
  const webhookUrl = `${origin}/api/webhooks/github`;
  const secretConfigured = Boolean(process.env.GITHUB_WEBHOOK_SECRET);
  return (
    <main className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-4 py-8 sm:px-6 lg:px-10">
      <header className="border-b border-border pb-8">
        <div className="flex h-9 items-center">
          <a
            href="https://rstream.io"
            target="_blank"
            rel="noreferrer"
            className="inline-flex text-foreground"
          >
            <RstreamLogo className="h-7 w-auto fill-current sm:h-8" />
          </a>
        </div>
        <div className="mt-10 max-w-4xl space-y-4">
          <p className="text-xs font-medium uppercase text-muted-foreground">
            rstream tunnels · Webhooks · Next.js
          </p>
          <h1
            className="text-4xl font-semibold text-foreground sm:text-5xl lg:text-6xl"
            id="page-title"
          >
            Webhook inbox.
          </h1>
          <p className="max-w-3xl text-base leading-8 text-muted-foreground sm:text-lg">
            Receive signed GitHub webhooks through this Next.js route and
            inspect the latest deliveries as they arrive.
          </p>
        </div>
      </header>
      <section
        className="flex flex-col gap-4 border-b border-border py-8"
        aria-labelledby="endpoint-heading"
      >
        <div className="flex items-end justify-between gap-6">
          <div>
            <p className="text-xs font-medium uppercase text-muted-foreground">
              Endpoint
            </p>
            <h2
              className="mt-1 text-2xl font-semibold text-foreground"
              id="endpoint-heading"
            >
              GitHub webhook URL
            </h2>
          </div>
        </div>
        <div className="flex min-w-0 flex-col gap-2 rounded-md border border-border bg-card px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
          <span className="shrink-0 text-xs font-medium uppercase text-muted-foreground">
            POST
          </span>
          <code className="min-w-0 [overflow-wrap:anywhere] font-mono text-sm">
            {webhookUrl}
          </code>
        </div>
        <p
          className={
            secretConfigured
              ? "text-sm leading-6 text-[#157347]"
              : "text-sm leading-6 text-muted-foreground"
          }
        >
          {secretConfigured
            ? "Ready to verify signed GitHub webhook deliveries."
            : "Set GITHUB_WEBHOOK_SECRET to enable signature verification before sending deliveries."}
        </p>
      </section>
      <section className="flex-1 py-8" aria-labelledby="deliveries">
        <div className="flex items-end justify-between gap-6">
          <div>
            <p className="text-xs font-medium uppercase text-muted-foreground">
              Recent deliveries
            </p>
            <h2
              className="mt-1 text-2xl font-semibold text-foreground"
              id="deliveries"
            >
              Deliveries
            </h2>
          </div>
          <span
            className={
              events.length > 0
                ? "inline-flex min-h-8 items-center rounded-full border border-[#157347]/45 px-3 py-1 text-sm font-medium text-[#157347]"
                : "inline-flex min-h-8 items-center rounded-full border border-[#996900]/45 px-3 py-1 text-sm font-medium text-[#996900]"
            }
          >
            {events.length > 0 ? `${events.length} received` : "Waiting"}
          </span>
        </div>
        {events.length === 0 ? (
          <div className="mt-5 grid gap-1 rounded-md border border-border bg-card px-4 py-8 leading-7 text-muted-foreground">
            <span className="font-semibold text-foreground">
              No deliveries yet.
            </span>
            <span>Send a signed GitHub webhook to the endpoint above.</span>
          </div>
        ) : (
          <div
            className="mt-5 overflow-hidden rounded-md border border-border bg-card"
            role="table"
          >
            <div className="overflow-x-auto">
              <div className="grid min-w-[58rem] grid-cols-[minmax(9rem,1.1fr)_minmax(11rem,1.2fr)_minmax(8rem,0.8fr)_minmax(12rem,1fr)_minmax(14rem,1fr)] items-center gap-4 bg-muted px-4 py-3 text-xs font-medium uppercase text-muted-foreground">
                <span role="columnheader">Event</span>
                <span role="columnheader">Repository</span>
                <span role="columnheader">Sender</span>
                <span role="columnheader">Delivery</span>
                <span role="columnheader">Received</span>
              </div>
              {events.map((event) => (
                <div
                  className="grid min-w-[58rem] grid-cols-[minmax(9rem,1.1fr)_minmax(11rem,1.2fr)_minmax(8rem,0.8fr)_minmax(12rem,1fr)_minmax(14rem,1fr)] items-center gap-4 border-t border-border px-4 py-3 text-sm"
                  key={event.delivery}
                  role="row"
                >
                  <span className="min-w-0 break-words" role="cell">
                    {event.event}
                    {event.action ? ` / ${event.action}` : ""}
                  </span>
                  <span className="min-w-0 break-words" role="cell">
                    {event.repository ?? "unknown repository"}
                  </span>
                  <span className="min-w-0 break-words" role="cell">
                    {event.sender ?? "unknown sender"}
                  </span>
                  <code
                    className="min-w-0 break-words font-mono text-xs"
                    role="cell"
                  >
                    {event.delivery}
                  </code>
                  <time
                    className="min-w-0 break-words"
                    role="cell"
                    dateTime={event.receivedAt}
                  >
                    {event.receivedAt}
                  </time>
                </div>
              ))}
            </div>
          </div>
        )}
      </section>
      <footer className="flex flex-col gap-3 border-t border-border pt-5 text-sm text-muted-foreground lg:flex-row lg:items-center lg:justify-between">
        <div className="flex min-w-0 flex-col gap-1 lg:flex-row lg:items-center lg:gap-5">
          <p className="lg:whitespace-nowrap">Powered by rstream tunnels.</p>
        </div>
        <nav
          className="flex flex-col gap-1 lg:flex-row lg:items-center lg:gap-5 lg:whitespace-nowrap"
          aria-label="Reference links"
        >
          <a
            href={GITHUB_URL}
            target="_blank"
            rel="noreferrer"
            className="font-medium text-foreground transition hover:text-muted-foreground"
          >
            Browse source code
          </a>
          <a
            href={GUIDE_URL}
            target="_blank"
            rel="noreferrer"
            className="font-medium text-foreground transition hover:text-muted-foreground"
          >
            Read the guide
          </a>
          <a
            href="https://rstream.io"
            target="_blank"
            rel="noreferrer"
            className="font-medium text-foreground transition hover:text-muted-foreground"
          >
            Visit rstream.io
          </a>
        </nav>
      </footer>
    </main>
  );
}

async function requestOrigin(): Promise<string> {
  const requestHeaders = await headers();
  const host =
    requestHeaders.get("x-forwarded-host") ??
    requestHeaders.get("host") ??
    "localhost:3000";
  const forwardedProto = requestHeaders.get("x-forwarded-proto");
  const protocol = forwardedProto ?? (isLocalHost(host) ? "http" : "https");
  return `${protocol}://${host}`;
}

function isLocalHost(host: string): boolean {
  return (
    host.startsWith("localhost") ||
    host.startsWith("127.0.0.1") ||
    host.startsWith("[::1]")
  );
}
