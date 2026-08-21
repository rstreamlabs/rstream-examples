import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"

import type { NextConfig } from "next"

const nextConfig: NextConfig = {
  turbopack: {
    root: resolve(dirname(fileURLToPath(import.meta.url)), ".."),
  },
}

export default nextConfig
