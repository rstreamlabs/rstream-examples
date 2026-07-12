export interface GithubIdentity {
  email?: string;
  login?: string;
}

export interface GithubAllowlist {
  emails: string[];
  domains: string[];
  logins: string[];
}

export function hasGithubAllowlist(allowlist: GithubAllowlist): boolean {
  return (
    allowlist.emails.length > 0 ||
    allowlist.domains.length > 0 ||
    allowlist.logins.length > 0
  );
}

export function isGithubIdentityAllowed(
  identity: GithubIdentity,
  allowlist: GithubAllowlist,
): boolean {
  if (!hasGithubAllowlist(allowlist)) return false;

  const email = identity.email?.toLowerCase() ?? "";
  const login = identity.login?.toLowerCase() ?? "";
  const domain = email.includes("@") ? (email.split("@")[1] ?? "") : "";

  return (
    (email !== "" && allowlist.emails.includes(email)) ||
    (domain !== "" && allowlist.domains.includes(domain)) ||
    (login !== "" && allowlist.logins.includes(login))
  );
}
