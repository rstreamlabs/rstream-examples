"use client"

import { X } from "lucide-react"
import { Close } from "@radix-ui/react-dialog"
import { Content } from "@radix-ui/react-dialog"
import { Description } from "@radix-ui/react-dialog"
import { Overlay } from "@radix-ui/react-dialog"
import { Portal } from "@radix-ui/react-dialog"
import { Root } from "@radix-ui/react-dialog"
import { Title } from "@radix-ui/react-dialog"
import { Trigger } from "@radix-ui/react-dialog"
import { type ComponentPropsWithoutRef } from "react"
import { type ElementRef } from "react"
import { forwardRef } from "react"

import { cn } from "@/lib/utils"

const Dialog = Root
const DialogTrigger = Trigger
const DialogClose = Close
const DialogPortal = Portal

const DialogOverlay = forwardRef<
  ElementRef<typeof Overlay>,
  ComponentPropsWithoutRef<typeof Overlay>
>(({ className, ...props }, ref) => (
  <Overlay
    ref={ref}
    className={cn(
      "fixed inset-0 z-50 bg-[#171512]/30 backdrop-blur-sm",
      className,
    )}
    {...props}
  />
))
DialogOverlay.displayName = Overlay.displayName

const DialogContent = forwardRef<
  ElementRef<typeof Content>,
  ComponentPropsWithoutRef<typeof Content>
>(({ className, children, ...props }, ref) => (
  <DialogPortal>
    <DialogOverlay />
    <Content
      ref={ref}
      className={cn(
        "fixed left-1/2 top-1/2 z-50 grid w-[calc(100%-2rem)] max-w-2xl -translate-x-1/2 -translate-y-1/2 gap-5 rounded-lg border border-border bg-card p-5 shadow-[0_24px_90px_rgba(23,21,18,0.18)]",
        className,
      )}
      {...props}
    >
      {children}
      <Close className="absolute right-4 top-4 rounded-md text-muted-foreground transition hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <X className="h-4 w-4" />
        <span className="sr-only">Close</span>
      </Close>
    </Content>
  </DialogPortal>
))
DialogContent.displayName = Content.displayName

const DialogHeader = ({
  className,
  ...props
}: ComponentPropsWithoutRef<"div">) => (
  <div className={cn("grid gap-2", className)} {...props} />
)
DialogHeader.displayName = "DialogHeader"

const DialogTitle = forwardRef<
  ElementRef<typeof Title>,
  ComponentPropsWithoutRef<typeof Title>
>(({ className, ...props }, ref) => (
  <Title
    ref={ref}
    className={cn("text-2xl font-semibold text-foreground", className)}
    {...props}
  />
))
DialogTitle.displayName = Title.displayName

const DialogDescription = forwardRef<
  ElementRef<typeof Description>,
  ComponentPropsWithoutRef<typeof Description>
>(({ className, ...props }, ref) => (
  <Description
    ref={ref}
    className={cn("text-sm leading-6 text-muted-foreground", className)}
    {...props}
  />
))
DialogDescription.displayName = Description.displayName

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
}
