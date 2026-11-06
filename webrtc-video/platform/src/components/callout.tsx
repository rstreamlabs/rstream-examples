// See LICENSE file in the project root for license information.

import { cn } from "@/lib/utils"
import * as React from "react"

export type CalloutType = "default" | "warning" | "danger"

interface CalloutProps extends React.HTMLAttributes<HTMLDivElement> {
  type?: CalloutType
}

interface CalloutTextProps extends React.HTMLAttributes<HTMLParagraphElement> {
  children: React.ReactNode
}

const calloutClassNames: Record<CalloutType, string> = {
  default: "border-green-950/20 bg-green-50 text-green-950",
  warning: "border-yellow-950/20 bg-yellow-50 text-yellow-950",
  danger: "border-red-950/20 bg-red-50 text-red-950",
}

const calloutButtonClassNames: Record<CalloutType, string> = {
  default:
    "border-green-950/25 bg-green-50 text-green-950 hover:bg-green-100 hover:text-green-950",
  warning:
    "border-yellow-950/25 bg-yellow-50 text-yellow-950 hover:bg-yellow-100 hover:text-yellow-950",
  danger:
    "border-red-950/25 bg-red-50 text-red-950 hover:bg-red-100 hover:text-red-950",
}

export function calloutActionButtonClassName(type: CalloutType): string {
  return calloutButtonClassNames[type]
}

export function Callout({
  type = "default",
  children,
  className,
  ...props
}: CalloutProps) {
  return (
    <div
      className={cn(
        "min-w-0 max-w-full rounded-lg border p-6",
        calloutClassNames[type],
        className,
      )}
      {...props}
    >
      {children}
    </div>
  )
}

export function CalloutTitle({
  children,
  className,
  ...props
}: CalloutTextProps) {
  return (
    <p className={cn("text-base font-semibold", className)} {...props}>
      {children}
    </p>
  )
}

export function CalloutDescription({
  children,
  className,
  ...props
}: CalloutTextProps) {
  return (
    <p
      className={cn(
        "min-w-0 break-words text-base text-current/75 [overflow-wrap:anywhere]",
        className,
      )}
      {...props}
    >
      {children}
    </p>
  )
}

export function CalloutBody({
  title,
  children,
}: {
  children?: React.ReactNode
  title: React.ReactNode
}) {
  return (
    <div className="min-w-0 space-y-2">
      <CalloutTitle>{title}</CalloutTitle>
      {children ? <CalloutDescription>{children}</CalloutDescription> : null}
    </div>
  )
}
