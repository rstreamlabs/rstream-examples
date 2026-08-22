# Distribution qualification evidence

Each directory records a clean-tree qualification result and the generated figures derived from it. The [`6115791`](./6115791/fanout/summary.json) record measures one shared producer source under one, four, and eight MediaMTX readers.

Regenerate the figure from its machine-readable summary with:

```bash
node ../fanout/render.mjs 6115791/fanout/summary.json 6115791/fanout/fanout.svg
```
