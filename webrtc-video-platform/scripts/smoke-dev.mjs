import { spawn } from "node:child_process"

const port = process.env.PORT ?? "3107"
const host = "127.0.0.1"
const url = `http://${host}:${port}/`

function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      stdio: ["ignore", "pipe", "pipe"],
      ...options,
    })
    let output = ""
    child.stdout.on("data", (chunk) => {
      output += chunk.toString()
    })
    child.stderr.on("data", (chunk) => {
      output += chunk.toString()
    })
    child.on("error", reject)
    child.on("exit", (code) => {
      if (code === 0) {
        resolve(output)
        return
      }
      reject(
        new Error(
          `${command} ${args.join(" ")} failed with ${code}\n${output}`,
        ),
      )
    })
  })
}

async function waitForHTTP(processHandle) {
  const deadline = Date.now() + 30_000
  let lastError
  while (Date.now() < deadline) {
    if (processHandle.exitCode !== null) {
      throw new Error(`next dev exited early with ${processHandle.exitCode}`)
    }
    try {
      const response = await fetch(url)
      const body = await response.text()
      if (!response.ok) {
        throw new Error(
          `GET / returned ${response.status}\n${body.slice(0, 1000)}`,
        )
      }
      if (!body.includes("Next.js WebRTC video platform")) {
        throw new Error("GET / did not render the public landing page")
      }
      return
    } catch (error) {
      lastError = error
      await new Promise((resolve) => setTimeout(resolve, 500))
    }
  }
  throw lastError ?? new Error("timed out waiting for next dev")
}

await run("npm", ["run", "prisma:generate"])

try {
  await waitForHTTP({ exitCode: null })
  console.log(`PASS existing dev server smoke: ${url}`)
  process.exit(0)
} catch {
  // No reusable server is listening on the requested URL. Start one below.
}

const next = spawn(
  "./node_modules/.bin/next",
  ["dev", "--hostname", host, "--port", port],
  {
    stdio: ["ignore", "pipe", "pipe"],
    env: process.env,
  },
)

let logs = ""
next.stdout.on("data", (chunk) => {
  logs += chunk.toString()
})
next.stderr.on("data", (chunk) => {
  logs += chunk.toString()
})

try {
  await waitForHTTP(next)
  console.log(`PASS public landing page smoke: ${url}`)
} catch (error) {
  console.error(logs)
  throw error
} finally {
  next.kill("SIGTERM")
  await new Promise((resolve) => next.once("exit", resolve))
}
