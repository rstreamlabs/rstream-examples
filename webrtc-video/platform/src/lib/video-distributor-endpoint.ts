export function mediaMTXWHEPURL(
  rawBaseURL: string,
  mediaPath: string,
  edgeToken?: string,
) {
  const url = new URL(rawBaseURL)
  const basePath = url.pathname.replace(/\/+$/, "")
  const relativePath = mediaPath.replace(/^\/+|\/+$/g, "")
  url.pathname = `${basePath}/${relativePath}/whep`
  if (edgeToken) {
    url.searchParams.set("rstream.token", edgeToken)
  }
  return url.toString()
}
