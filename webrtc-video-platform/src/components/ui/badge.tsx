import { type HTMLAttributes } from "react"

import { cn } from "@/lib/utils"

type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  tone?: "online" | "offline" | "neutral"
}

export function Badge({ className, tone = "neutral", ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex h-7 items-center rounded-md border px-2.5 text-xs font-medium",
        tone === "online" &&
          "border-emerald-200 bg-emerald-50 text-emerald-800",
        tone === "offline" &&
          "border-border bg-background text-muted-foreground",
        tone === "neutral" && "border-border bg-background text-foreground",
        className,
      )}
      {...props}
    />
  )
}
