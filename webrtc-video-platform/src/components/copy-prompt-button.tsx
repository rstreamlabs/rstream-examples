"use client"

import { Check } from "lucide-react"
import { Copy } from "lucide-react"
import { useCallback } from "react"
import { useState } from "react"

import { Button } from "@/components/ui/button"

type CopyState = "idle" | "copied" | "failed"

export function CopyPromptButton({
  className,
  prompt,
  label = "Copy setup prompt",
}: {
  className?: string
  prompt: string
  label?: string
}) {
  const [state, setState] = useState<CopyState>("idle")
  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(prompt)
      setState("copied")
      window.setTimeout(() => setState("idle"), 1500)
    } catch {
      setState("failed")
      window.setTimeout(() => setState("idle"), 1500)
    }
  }, [prompt])
  return (
    <Button
      type="button"
      variant="outline"
      className={className}
      onClick={onCopy}
    >
      {state === "copied" ? (
        <Check className="h-4 w-4" />
      ) : (
        <Copy className="h-4 w-4" />
      )}
      {state === "failed" ? "Copy failed" : label}
    </Button>
  )
}
