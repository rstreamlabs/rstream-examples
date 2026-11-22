import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import test from "node:test";

const parserRequire = createRequire(
  import.meta.resolve("@typescript-eslint/typescript-estree"),
);
const matcherPath = parserRequire.resolve("minimatch");

test("TypeScript tooling retains recursive, negated and Windows glob matching", () => {
  const { minimatch } = parserRequire("minimatch");
  assert.equal(minimatch("src/views/page.tsx", "src/**/*.tsx"), true);
  assert.equal(minimatch("src/views/page.test.tsx", "!**/*.test.tsx"), false);
  assert.equal(minimatch("src/views/page.tsx", "!**/*.test.tsx"), true);
  assert.equal(
    minimatch("C:/project/src/page.ts", "C:/**/src/*.ts", {
      platform: "win32",
    }),
    true,
  );
});

test("selected TypeScript matcher bounds adversarial preprocessing", () => {
  const probe = `const { Minimatch } = require(process.argv[1]); const pattern = Array(12).fill("**/../src/pkg").join("/"); try { const matcher = new Minimatch(pattern, { optimizationLevel: 2, braceExpandMax: 32 }); if (matcher.set.length > 32) process.exit(1); } catch (error) { if (!(error instanceof RangeError) || !error.message.includes("too many patterns")) throw error; }`;
  execFileSync(
    process.execPath,
    ["--max-old-space-size=96", "-e", probe, matcherPath],
    { timeout: 3000, stdio: "pipe" },
  );
});

const mathRequire = createRequire(import.meta.resolve("@streamdown/math"));

test("math renderer keeps inherited options from enabling unsafe links", () => {
  const katex = mathRequire("katex");
  const options = Object.create({ trust: true });
  const output = katex.renderToString(
    "\\href{javascript:alert(1)}{click}",
    options,
  );
  assert.doesNotMatch(output, /href="javascript:/);
  assert.match(
    katex.renderToString("c^2 = a^2 + b^2", { displayMode: true }),
    /katex-display/,
  );
});

test("math plugin renders through the actual Streamdown integration", async () => {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { Streamdown } = await import("streamdown");
  const { math } = await import("@streamdown/math");
  const output = renderToStaticMarkup(
    createElement(Streamdown, {
      plugins: { math },
      children: "$$\na^2 + b^2\n$$",
      mode: "static",
    }),
  );
  assert.match(output, /class="katex"/);
  assert.match(output, /<math/);
});
