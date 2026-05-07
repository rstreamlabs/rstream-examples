import NextAuth from "next-auth"

import { authOptions } from "@/lib/next-auth"

const handler = NextAuth(authOptions)
const GET = handler
const POST = handler

export { GET, POST }
