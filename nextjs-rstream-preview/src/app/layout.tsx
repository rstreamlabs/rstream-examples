import type { Metadata } from "next";
import type { ReactNode } from "react";

import "@/styles/globals.css";

export const metadata: Metadata = {
  title: "Next.js rstream preview",
  description: "A Next.js preview app served through rstream.",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
