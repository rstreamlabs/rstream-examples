import { Chat } from "@/components/chat";
import { RstreamLogo } from "@/components/rstream-logo";
import { SignIn } from "@/components/sign-in";
import { SignOutButton } from "@/components/sign-out-button";
import { availableAuth, getServerUser } from "@/lib/auth";
import { rstreamEnv } from "@/lib/env";

const GITHUB_URL =
  "https://github.com/rstreamlabs/rstream-examples/tree/main/private-llm-mesh";

export default async function Page() {
  const user = await getServerUser();
  const auth = availableAuth();
  const projectEndpoint = rstreamEnv().RSTREAM_PROJECT_ENDPOINT ?? "";

  return (
    <div className="flex min-h-dvh w-full flex-col px-4 py-3 sm:px-6 sm:py-4 lg:px-10 [@media(min-height:640px)]:h-dvh">
      <header className="shrink-0 border-b border-border pb-3">
        <div className="flex h-9 items-center justify-between gap-6">
          <a
            href="https://rstream.io"
            target="_blank"
            rel="noreferrer"
            className="inline-flex text-foreground"
          >
            <RstreamLogo className="h-7 w-auto fill-current sm:h-8" />
          </a>
          <nav className="flex items-center gap-4 text-xs">
            <a
              href={GITHUB_URL}
              target="_blank"
              rel="noreferrer"
              className="hidden text-muted-foreground transition hover:text-foreground sm:inline"
            >
              Source code
            </a>
            <span className="hidden text-muted-foreground sm:inline">
              private-llm-mesh
            </span>
            {user && !auth.disabled ? <SignOutButton /> : null}
          </nav>
        </div>
      </header>

      {user ? (
        <Chat projectEndpoint={projectEndpoint} />
      ) : (
        <section className="flex flex-1 items-center justify-center px-4">
          <div className="w-full max-w-md space-y-6 text-center">
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Local models · your machines
            </p>
            <h1 className="text-3xl font-semibold text-foreground sm:text-4xl">
              Chat with open models running on your own machines.
            </h1>
            <p className="text-base leading-8 text-muted-foreground">
              Sign in to reach your worker pool. Each message streams straight
              from one of your machines — no model leaves your hardware.
            </p>
            <div className="flex justify-center pt-2">
              <SignIn github={auth.github} />
            </div>
          </div>
        </section>
      )}
    </div>
  );
}
