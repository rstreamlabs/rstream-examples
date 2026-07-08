"use client";

import { signIn } from "next-auth/react";

import { Button } from "@/components/ui/button";

export function SignIn({ github }: { github: boolean }) {
  if (!github) {
    return (
      <p className="max-w-md text-sm text-destructive">
        No sign-in method is configured. Set GITHUB_CLIENT_ID and
        GITHUB_CLIENT_SECRET, or AUTH_DISABLED=true for a local quickstart.
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
