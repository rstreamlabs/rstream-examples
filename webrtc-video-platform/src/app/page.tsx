import { DeviceDashboard } from "@/components/device-dashboard"
import { deviceViews } from "@/lib/devices"
import { getServerUser } from "@/lib/next-auth"
import { RstreamLogo } from "@/components/rstream-logo"
import { SignInButton } from "@/components/sign-in-button"
import { SignOutButton } from "@/components/sign-out-button"

const GUIDE_URL =
  "https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream"
const GITHUB_URL =
  "https://github.com/rstreamlabs/rstream-examples/tree/main/webrtc-video-platform"

export default async function Page() {
  const user = await getServerUser()
  if (!user?.id) {
    return (
      <main className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-6 py-8 lg:px-10">
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
        </header>
        <section className="flex flex-1 items-center py-16">
          <div className="max-w-2xl space-y-6">
            <p className="text-xs font-medium uppercase text-muted-foreground">
              rstream tunnels · WebRTC · Next.js
            </p>
            <h1 className="text-4xl font-semibold text-foreground sm:text-6xl">
              Next.js WebRTC video platform.
            </h1>
            <p className="max-w-2xl text-base leading-8 text-muted-foreground sm:text-lg">
              Provision devices, issue short-lived viewer access, and stream
              through rstream tunnels from a Next.js app.
            </p>
            <SignInButton />
          </div>
        </section>
        <PageFooter />
      </main>
    )
  }
  const devices = await deviceViews(user.id)
  return (
    <main className="mx-auto flex min-h-screen w-full max-w-7xl flex-col px-6 py-8 lg:px-10">
      <header className="border-b border-border pb-8">
        <div className="flex h-9 items-center justify-between gap-6">
          <a
            href="https://rstream.io"
            target="_blank"
            rel="noreferrer"
            className="inline-flex text-foreground"
          >
            <RstreamLogo className="h-7 w-auto fill-current sm:h-8" />
          </a>
          <SignOutButton />
        </div>
        <div className="mt-10 max-w-4xl space-y-4">
          <p className="text-xs font-medium uppercase text-muted-foreground">
            rstream tunnels · WebRTC · Next.js
          </p>
          <h1 className="text-4xl font-semibold text-foreground sm:text-5xl lg:text-6xl">
            Next.js WebRTC video platform.
          </h1>
          <p className="max-w-3xl text-base leading-8 text-muted-foreground sm:text-lg">
            Provision devices, issue short-lived viewer access, and stream
            through rstream tunnels from a Next.js app.
          </p>
        </div>
      </header>
      <section className="flex-1 py-8">
        <DeviceDashboard initialDevices={devices} />
      </section>
      <PageFooter account={user.email ?? user.name ?? user.id} />
    </main>
  )
}

function PageFooter({ account }: { account?: string }) {
  return (
    <footer className="flex flex-col gap-3 border-t border-border pt-5 text-sm text-muted-foreground lg:flex-row lg:items-center lg:justify-between">
      <div className="flex min-w-0 flex-col gap-1 lg:flex-row lg:items-center lg:gap-5">
        <p className="lg:whitespace-nowrap">Powered by rstream tunnels.</p>
        {account ? (
          <p className="break-all lg:truncate lg:break-normal">
            Signed in as {account}.
          </p>
        ) : null}
      </div>
      <div className="flex flex-col gap-1 lg:flex-row lg:items-center lg:gap-5 lg:whitespace-nowrap">
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
      </div>
    </footer>
  )
}
