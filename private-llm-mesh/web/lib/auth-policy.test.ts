import assert from "node:assert/strict";
import { test } from "node:test";

import {
  hasGithubAllowlist,
  isGithubIdentityAllowed,
  type GithubAllowlist,
} from "./auth-policy.ts";

const empty: GithubAllowlist = { emails: [], domains: [], logins: [] };

test("GitHub authentication fails closed without an allowlist", () => {
  assert.equal(hasGithubAllowlist(empty), false);
  assert.equal(
    isGithubIdentityAllowed(
      { email: "alice@example.com", login: "alice" },
      empty,
    ),
    false,
  );
});

test("GitHub identities can be allowed by email, domain, or login", () => {
  assert.equal(
    isGithubIdentityAllowed(
      { email: "ALICE@example.com", login: "unlisted" },
      { emails: ["alice@example.com"], domains: [], logins: [] },
    ),
    true,
  );
  assert.equal(
    isGithubIdentityAllowed(
      { email: "bob@EXAMPLE.COM", login: "unlisted" },
      { emails: [], domains: ["example.com"], logins: [] },
    ),
    true,
  );
  assert.equal(
    isGithubIdentityAllowed(
      { email: "other@example.net", login: "CAROL" },
      { emails: [], domains: [], logins: ["carol"] },
    ),
    true,
  );
});

test("GitHub identities outside the allowlist are rejected", () => {
  assert.equal(
    isGithubIdentityAllowed(
      { email: "mallory@example.net", login: "mallory" },
      {
        emails: ["alice@example.com"],
        domains: ["example.org"],
        logins: ["bob"],
      },
    ),
    false,
  );
});
