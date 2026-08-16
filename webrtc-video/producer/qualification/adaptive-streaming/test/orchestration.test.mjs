import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { fileURLToPath } from "node:url";

test("requires an explicit CLI context before touching external runtimes", () => {
  const script = fileURLToPath(new URL("../run.sh", import.meta.url));
  const producerDirectory = fileURLToPath(new URL("../../..", import.meta.url));
  const result = spawnSync(script, [], {
    cwd: producerDirectory,
    encoding: "utf8",
    env: { ...process.env, RSTREAM_CONTEXT: "" },
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /RSTREAM_CONTEXT must name/);
  assert.doesNotMatch(result.stderr, /required command not found/);
});

test("bounds the host scheduling sampler interval", () => {
  const script = fileURLToPath(
    new URL("../sample-host-cpu.sh", import.meta.url),
  );
  for (const interval of ["0", "0250", "60001", "invalid"]) {
    const result = spawnSync(script, ["unused", interval], {
      encoding: "utf8",
    });
    assert.notEqual(result.status, 0, `accepted interval ${interval}`);
    assert.match(result.stderr, /interval-milliseconds/);
  }
});

test("accepts a prepared runtime without requiring the CLI or Go", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_PREPARED_RUNTIME_DIRECTORY:-/);
  assert.match(
    runScript,
    /install -m 0600 \\\n+      "\$\{prepared_runtime_directory\}\/\$\{runtime_file\}"/,
  );
  assert.match(
    runScript,
    /if \[\[ -z "\$\{context_name\}" && -z "\$\{prepared_runtime_directory\}" \]\]/,
  );
  assert.doesNotMatch(
    runScript,
    /for command in docker git jq node rstream tar/,
  );
});

test("does not retain local context identifiers in qualification evidence", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.doesNotMatch(runScript, /rstream context get/);
  assert.doesNotMatch(runScript, /--arg context /);
  assert.doesNotMatch(runScript, /projectEndpoint: \$project_endpoint/);
  assert.doesNotMatch(
    runScript,
    /Preparing credential-isolated runtime context %s/,
  );
});

test("writes browser evidence as the controlling host user", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /browser_user="\$\(id -u\):\$\(id -g\)"/);
  assert.match(
    runScript,
    /browser_arguments=\(\n+  --name "\$\{browser_container_name\}"\n+  --user "\$\{browser_user\}"/,
  );
  assert.match(
    runScript,
    /producer_docker run --detach \\\n+  --name "\$\{container_name\}" \\\n+  --user 0:0/,
  );
});

test("stops namespace samplers before completing the collector", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  const recovery = runScript.lastIndexOf("write_phase recovery '{}'");
  const stopSamplers = runScript.lastIndexOf("stop_udp_samplers");
  const complete = runScript.lastIndexOf("write_phase complete '{}'");
  const waitCollector = runScript.lastIndexOf('wait "${collector_pid}"');
  assert.ok(recovery >= 0, "recovery phase invocation is missing");
  assert.ok(
    recovery < stopSamplers &&
      stopSamplers < complete &&
      complete < waitCollector,
    "samplers must stop before the complete phase lets their container exit",
  );
});

test("records and applies one configurable qdisc queue limit", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  const trafficControlScript = await readFile(
    fileURLToPath(new URL("../traffic-control.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_QUEUE_LIMIT_PACKETS:-256/);
  assert.match(
    trafficControlScript,
    /limit "\$\{queue_limit_packets\}" rate "\$\{rate_kbps\}kbit"/,
  );
  assert.doesNotMatch(runScript, /queueLimitPackets: 128/);
});

test("runs impairment timing inside the producer namespace", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /start_traffic_control/);
  assert.match(
    runScript,
    /wait_for_traffic_control_event constrained-started 15/,
  );
  assert.match(
    runScript,
    /wait_for_traffic_control_event impaired-started \$\(\(constrained_seconds \+ 15\)\)/,
  );
  assert.match(
    runScript,
    /wait_for_traffic_control_event recovery-started \$\(\(impaired_seconds \+ 15\)\)/,
  );
  assert.match(runScript, /capture_network_evidence/);
  assert.match(
    runScript,
    /tar -C \/tmp\/rstream-network-evidence -cf - \. \| \\\n+    tar -C "\$\{output_directory\}" -xf -/,
  );
  assert.doesNotMatch(
    runScript,
    /producer_docker cp[\s\S]+rstream-network-evidence/,
  );
  assert.doesNotMatch(runScript, /capture_qdisc\(\)/);
  assert.doesNotMatch(runScript, /apply_shaping\(\)/);
});

test("passes and records the configured FlexFEC protection ratio", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_FLEXFEC_MEDIA_PACKETS:-4/);
  assert.match(runScript, /RSTREAM_QUALIFICATION_FLEXFEC_REPAIR_PACKETS:-2/);
  assert.match(
    runScript,
    /"-flex-fec-media-packets=\$\{flexfec_media_packets\}"/,
  );
  assert.match(
    runScript,
    /"-flex-fec-repair-packets=\$\{flexfec_repair_packets\}"/,
  );
  assert.match(
    runScript,
    /flexFECMediaPackets: \(if \$flexfec_enabled then \$flexfec_media_packets else 0 end\)/,
  );
  assert.match(
    runScript,
    /flexFECRepairPackets: \(if \$flexfec_enabled then \$flexfec_repair_packets else 0 end\)/,
  );
  assert.match(
    runScript,
    /if \[\[ "\$\{flexfec_enabled\}" == "true" \]\]; then\n+    producer_config_path="\$\{producer_directory\}\/config\.test-pattern\.h264\.twcc-gcc-flexfec\.yaml"/,
  );
  assert.match(runScript, /-producer-config "\$\{producer_config_path\}"/);
  assert.doesNotMatch(
    runScript,
    /-producer-config "\$\{producer_directory\}\/config\.test-pattern\.h264\.twcc-gcc\.yaml"/,
  );
});

test("runs the context helper from the module or a prebuilt binary", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(
    runScript,
    /go -C "\$\{producer_directory\}" run \\[^]*?\.\/qualification\/adaptive-streaming\/cmd\/prepare-context \\[^]*?"\$\{prepare_context_arguments\[@\]\}"/,
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_PREPARE_CONTEXT_BINARY:-/);
  assert.match(
    runScript,
    /"\$\{prepare_context_binary\}" "\$\{prepare_context_arguments\[@\]\}"/,
  );
  assert.match(
    runScript,
    /elif \[\[ -z "\$\{prepare_context_binary\}" \]\]; then\n+  require_command rstream\n+  require_command go/,
  );
  assert.doesNotMatch(
    runScript,
    /for command in docker git go jq node npm rstream tar/,
  );
  assert.doesNotMatch(
    runScript,
    /go run "\$\{script_directory\}\/cmd\/prepare-context"/,
  );
});

test("records producer and browser runtimes without a user or host identifier", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.doesNotMatch(runScript, /uname -a/);
  assert.doesNotMatch(runScript, /--arg host/);
  assert.match(runScript, /producer_docker info --format '\{\{json \.\}\}'/);
  assert.match(runScript, /browser_docker_info="\$\(docker info/);
  assert.match(runScript, /producerLocation: \$producer_location/);
  assert.match(runScript, /\.browserRuntime = \{/);
});

test("keeps one qualification path when the producer uses another Docker daemon", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_PRODUCER_DOCKER_CONTEXT:-/);
  assert.match(runScript, /RSTREAM_QUALIFICATION_PRODUCER_DOCKER_HOST:-/);
  assert.match(
    runScript,
    /RSTREAM_QUALIFICATION_PLAYOUT_DELAY_HINT_SECONDS:-0/,
  );
  assert.match(runScript, /--playout-delay-hint-seconds/);
  assert.match(runScript, /producer_docker build/);
  assert.match(runScript, /producer_docker run --detach/);
  assert.match(runScript, /producer_docker exec --interactive --user 0/);
  assert.match(runScript, /producer_docker stop --time 5/);
  assert.match(
    runScript,
    /direct qualification requires producer and browser on the same Docker daemon/,
  );
  assert.match(
    runScript,
    /type=volume,source=\$\{producer_runtime_volume\},target=\/runtime,readonly/,
  );
  assert.match(runScript, /docker build "\$\{pull_arguments\[@\]\}"/);
  assert.match(runScript, /docker run \\\n  "\$\{browser_arguments\[@\]\}"/);
});

test("captures verbose encoder evidence outside the live host mount", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(
    runScript,
    /GST_DEBUG_FILE=\/tmp\/rstream-qualification-encoder\.log/,
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_ENCODER_DEBUG:-true/);
  assert.match(
    runScript,
    /docker exec "\$\{container_name\}" sh -c \\\n+    'test -s \/tmp\/rstream-qualification-encoder\.log && cat \/tmp\/rstream-qualification-encoder\.log'/,
  );
  assert.doesNotMatch(runScript, /qualification-evidence\/encoder\.log/);
  const mediaStop = runScript.lastIndexOf(
    "grep -q 'GStreamer pipeline stopped'",
  );
  const copyEvidence = runScript.lastIndexOf("if ! capture_encoder_evidence");
  const stopProducer = runScript.lastIndexOf(
    'docker stop --time 5 "${container_name}"',
  );
  assert.ok(
    mediaStop >= 0 && mediaStop < copyEvidence && copyEvidence < stopProducer,
    "encoder evidence must be closed and copied before the tmpfs is removed",
  );
});

test("captures scheduler evidence from both media hosts", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  const dockerfile = await readFile(
    fileURLToPath(new URL("../Dockerfile", import.meta.url)),
    "utf8",
  );
  const browserDockerfile = await readFile(
    fileURLToPath(new URL("../Browser.Dockerfile", import.meta.url)),
    "utf8",
  );
  const dockerignore = await readFile(
    fileURLToPath(new URL("../../../.dockerignore", import.meta.url)),
    "utf8",
  );
  assert.match(
    dockerfile,
    /COPY qualification\/adaptive-streaming\/sample-host-cpu\.sh \/usr\/local\/bin\/rstream-sample-host-cpu/,
  );
  assert.match(
    browserDockerfile,
    /COPY sample-host-cpu\.sh \/usr\/local\/bin\/rstream-sample-host-cpu/,
  );
  assert.match(
    dockerignore,
    /!qualification\/adaptive-streaming\/sample-host-cpu\.sh/,
  );
  assert.match(runScript, /start_host_cpu_sampler/);
  assert.match(runScript, /stop_host_cpu_sampler/);
  assert.match(runScript, /capture_host_cpu_evidence/);
  assert.match(runScript, /start_receiver_host_cpu_sampler/);
  assert.match(runScript, /stop_receiver_host_cpu_sampler/);
  assert.match(runScript, /receiver-host-cpu\.jsonl/);
  assert.match(
    runScript,
    /browserRuntime = \{[^]*?logicalCPUs: \$logical_cpus/,
  );
  assert.match(
    runScript,
    /wait_for_file "\$\{output_directory\}\/collector-ready\.json" 120\nstart_receiver_host_cpu_sampler\nwait_for_file "\$\{output_directory\}\/receiver-host-cpu\.jsonl" 10/,
  );
  assert.match(
    runScript,
    /test -s \/tmp\/rstream-qualification-host-cpu\.jsonl && cat \/tmp\/rstream-qualification-host-cpu\.jsonl/,
  );
  assert.doesNotMatch(runScript, /--arg host_cpu/);
});

test("switches the producer interface only in an explicit relay mobility run", async () => {
  const runScript = await readFile(
    fileURLToPath(new URL("../run.sh", import.meta.url)),
    "utf8",
  );
  assert.match(runScript, /RSTREAM_QUALIFICATION_MOBILITY:-off/);
  assert.match(
    runScript,
    /producer mobility qualification requires the relay path/,
  );
  assert.match(
    runScript,
    /producer_arguments\+=\(--env RSTREAM_TUNNEL_TRANSPORT=quic\)/,
  );
  assert.match(runScript, /signalingTransport: "quic"/);
  assert.match(
    runScript,
    /producer_docker network connect \\\n+    "\$\{producer_secondary_network\}" "\$\{container_name\}"/,
  );
  assert.match(
    runScript,
    /producer_docker network disconnect \\\n+    "\$\{producer_primary_network\}" "\$\{container_name\}"/,
  );
  const baseline = runScript.lastIndexOf(
    'run_phase baseline "${baseline_seconds}"',
  );
  const mobility = runScript.lastIndexOf("write_phase mobility");
  const trafficControl = runScript.lastIndexOf("start_traffic_control");
  assert.ok(
    baseline >= 0 && baseline < mobility && mobility < trafficControl,
    "mobility must follow the healthy baseline and precede impairment",
  );
});
