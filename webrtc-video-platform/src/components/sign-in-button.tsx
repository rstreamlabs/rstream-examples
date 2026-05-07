"use client"

import { signIn } from "next-auth/react"

import { Button } from "@/components/ui/button"

export function SignInButton() {
  return (
    <Button
      onClick={() => signIn("github", undefined, { prompt: "select_account" })}
    >
      Continue with GitHub
    </Button>
  )
}
