import { DeviceDashboard } from "@/components/device-dashboard"
import { deviceViews } from "@/lib/devices"
import { getServerUser } from "@/lib/next-auth"
import { RstreamLogo } from "@/components/rstream-logo"
import { SignInButton } from "@/components/sign-in-button"
import { SignOutButton } from "@/components/sign-out-button"

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
        <footer className="flex flex-col gap-3 border-t border-border pt-5 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
          <p>Powered by rstream tunnels.</p>
          <a
            href="https://rstream.io"
            target="_blank"
            rel="noreferrer"
            className="font-medium text-foreground transition hover:text-muted-foreground"
          >
            Visit rstream.io
          </a>
        </footer>
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
      <footer className="flex flex-col gap-3 border-t border-border pt-5 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
        <p>Powered by rstream tunnels.</p>
        <a
          href="https://rstream.io"
          target="_blank"
          rel="noreferrer"
          className="font-medium text-foreground transition hover:text-muted-foreground"
        >
          Visit rstream.io
        </a>
      </footer>
    </main>
  )
}
