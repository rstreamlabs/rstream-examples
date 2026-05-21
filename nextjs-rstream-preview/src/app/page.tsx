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
    <main className="page">
      <header className="page-header">
        <div className="logo-row">
          <a
            href="https://rstream.io"
            target="_blank"
            rel="noreferrer"
            className="logo-link"
          >
            <RstreamLogo className="logo" />
          </a>
        </div>
        <div className="inbox-header">
          <p className="eyebrow">rstream tunnels · Webhooks · Next.js</p>
          <h1 className="title" id="page-title">
            Webhook inbox.
          </h1>
          <p className="lede">
            Receive signed GitHub webhooks through this Next.js route and
            inspect the latest deliveries as they arrive.
          </p>
        </div>
      </header>
      <section className="endpoint-panel" aria-labelledby="endpoint-heading">
        <div className="section-header">
          <div>
            <p className="panel-label">Endpoint</p>
            <h2 className="section-heading" id="endpoint-heading">
              GitHub webhook URL
            </h2>
          </div>
        </div>
        <div className="endpoint-row">
          <span>POST</span>
          <code>{webhookUrl}</code>
        </div>
        <p
          className={secretConfigured ? "endpoint-note ready" : "endpoint-note"}
        >
          {secretConfigured
            ? "Ready to verify signed GitHub webhook deliveries."
            : "Set GITHUB_WEBHOOK_SECRET to enable signature verification before sending deliveries."}
        </p>
      </section>
      <section className="deliveries-section" aria-labelledby="deliveries">
        <div className="section-header">
          <div>
            <p className="panel-label">Recent deliveries</p>
            <h2 className="section-heading" id="deliveries">
              Deliveries
            </h2>
          </div>
          <span className={events.length > 0 ? "status live" : "status"}>
            {events.length > 0 ? `${events.length} received` : "Waiting"}
          </span>
        </div>
        {events.length === 0 ? (
          <div className="empty-state">
            <span>No deliveries yet.</span>
            <span>Send a signed GitHub webhook to the endpoint above.</span>
          </div>
        ) : (
          <div className="delivery-table" role="table">
            <div className="delivery-row delivery-head" role="row">
              <span role="columnheader">Event</span>
              <span role="columnheader">Repository</span>
              <span role="columnheader">Sender</span>
              <span role="columnheader">Delivery</span>
              <span role="columnheader">Received</span>
            </div>
            {events.map((event) => (
              <div className="delivery-row" key={event.delivery} role="row">
                <span role="cell">
                  {event.event}
                  {event.action ? ` / ${event.action}` : ""}
                </span>
                <span role="cell">
                  {event.repository ?? "unknown repository"}
                </span>
                <span role="cell">{event.sender ?? "unknown sender"}</span>
                <code role="cell">{event.delivery}</code>
                <time role="cell" dateTime={event.receivedAt}>
                  {event.receivedAt}
                </time>
              </div>
            ))}
          </div>
        )}
      </section>
      <footer className="page-footer">
        <div className="footer-copy">
          <p>Powered by rstream tunnels.</p>
        </div>
        <nav className="footer-links" aria-label="Reference links">
          <a href={GITHUB_URL} target="_blank" rel="noreferrer">
            Browse source code
          </a>
          <a href={GUIDE_URL} target="_blank" rel="noreferrer">
            Read the guide
          </a>
          <a href="https://rstream.io" target="_blank" rel="noreferrer">
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
