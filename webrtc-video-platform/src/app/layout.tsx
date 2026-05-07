import { type Metadata } from "next"

import "@/styles/globals.css"

export const metadata: Metadata = {
  title: "Next.js WebRTC Video Platform",
  description: "Reference Next.js platform for rstream-powered WebRTC streams.",
}

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode
}>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  )
}
