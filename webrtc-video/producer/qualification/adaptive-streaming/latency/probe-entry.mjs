import { installLatencyProbe } from "./latency-probe.mjs";

window.__rstreamLatencyProbe?.stop();
window.__rstreamLatencyProbe = installLatencyProbe(
  document.querySelector("#video"),
  window.__rstreamLatencyClockOffsetMilliseconds,
);
