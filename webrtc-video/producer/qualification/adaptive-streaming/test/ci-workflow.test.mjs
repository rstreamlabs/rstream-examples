import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";

const workflowPath = fileURLToPath(
  new URL(
    "../../../../../.github/workflows/video-qualification.yml",
    import.meta.url,
  ),
);

test("keeps qualification credentials out of the reusable workflow", async () => {
  const workflow = await readFile(workflowPath, "utf8");
  assert.doesNotMatch(workflow, /env-dev-pro|aws-global-dev|dev\.rstream\.io/);
  assert.match(
    workflow,
    /RSTREAM_AUTHENTICATION_TOKEN: \$\{\{ secrets\.RSTREAM_QUALIFICATION_TOKEN \}\}/,
  );
  assert.match(
    workflow,
    /RSTREAM_QUALIFICATION_CONFIG_B64: \$\{\{ secrets\.RSTREAM_QUALIFICATION_CONFIG_B64 \}\}/,
  );
  assert.match(workflow, /base64 --decode/);
  assert.doesNotMatch(workflow, /\$\{\{ vars\./);
  assert.match(
    workflow,
    /RSTREAM_QUALIFICATION_PREPARED_RUNTIME_DIRECTORY: \$\{\{ steps\.runtime\.outputs\.runtime_directory \}\}/,
  );
  const jobHeader = workflow.match(/jobs:\n[\s\S]*?steps:/)?.[0] || "";
  assert.doesNotMatch(jobHeader, /RSTREAM_AUTHENTICATION_TOKEN/);
  assert.doesNotMatch(jobHeader, /RSTREAM_QUALIFICATION_CONFIG_B64/);
});

test("pins external actions and preserves failed-run evidence", async () => {
  const workflow = await readFile(workflowPath, "utf8");
  const uses = [...workflow.matchAll(/^\s*uses:\s*([^\s#]+)/gm)].map(
    (match) => match[1],
  );
  assert.ok(uses.length >= 3);
  for (const action of uses) {
    assert.match(action, /^[^@]+@[0-9a-f]{40}$/);
  }
  assert.match(workflow, /continue-on-error: true/);
  assert.match(
    workflow,
    /if: always\(\) && steps\.runtime\.outcome == 'success'/,
  );
  assert.match(
    workflow,
    /steps\.qualification\.outcome.*!=.*success/,
  );
});

test("rejects mobility on a direct path and keeps FlexFEC optional", async () => {
  const workflow = await readFile(workflowPath, "utf8");
  assert.match(
    workflow,
    /\[\[ "\$INPUT_PATH" != "relay" && "\$mobility" != "off" \]\]/,
  );
  assert.match(
    workflow,
    /\[\[ "\$INPUT_PROTECTION" == "nack-rtx-flexfec" \]\]/,
  );
  assert.match(workflow, /prepare_arguments\+=\(/);
});
