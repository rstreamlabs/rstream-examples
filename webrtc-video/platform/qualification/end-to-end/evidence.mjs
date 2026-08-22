import { expectedBrowserDiagnostic } from "./diagnostics.mjs"

export async function drainBrowserEvents(page, destination) {
  if (!page || page.isClosed()) {
    return
  }
  const observed = await page
    .evaluate(() => {
      const current = Array.isArray(window.__rstreamQualificationEvents)
        ? window.__rstreamQualificationEvents
        : []
      window.__rstreamQualificationEvents = []
      return current
    })
    .catch(() => [])
  destination.push(...observed)
}

export function unexpectedBrowserDiagnostics(diagnostics) {
  return diagnostics.filter(
    (diagnostic) => !expectedBrowserDiagnostic(diagnostic),
  )
}
