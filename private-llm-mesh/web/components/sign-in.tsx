"use client";

import { signIn } from "next-auth/react";

import { Button } from "@/components/ui/button";

export function SignIn({ github }: { github: boolean }) {
  if (!github) {
    return (
      <p className="max-w-md text-sm text-destructive">
        No sign-in method is configured. Set the GitHub credentials and at least
        one allowlist, or AUTH_DISABLED=true for a local quickstart.
      </p>
    );
  }
  return (
    <Button
      size="lg"
      onClick={() =>
        void signIn("github", undefined, { prompt: "select_account" })
      }
    >
      Continue with GitHub
    </Button>
  );
}
