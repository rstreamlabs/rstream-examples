type SourceCredentialIssuers<Source, Destination, TURN> = {
  issueSource: () => Promise<Source>
  issueDestination: () => Promise<Destination>
  issueTURN: () => Promise<TURN>
}

export async function issueSourceCredentials<Source, Destination, TURN>(
  purpose: "session" | "signaling",
  issuers: SourceCredentialIssuers<Source, Destination, TURN>,
) {
  const turn =
    purpose === "session" ? issuers.issueTURN() : Promise.resolve(null)
  const [source, destination, ice] = await Promise.all([
    issuers.issueSource(),
    issuers.issueDestination(),
    turn,
  ])
  return { source, destination, turn: ice }
}
