// Dev helper: mint a prod worker token + resolve the engine from the app creds,
// so a worker can register on the same project without changing the CLI login.
import { RstreamTunnelsClient } from "@rstreamlabs/tunnels";
const c = new RstreamTunnelsClient({
  credentials: {
    clientId: process.env.RSTREAM_CLIENT_ID,
    clientSecret: process.env.RSTREAM_CLIENT_SECRET,
  },
  projectId: process.env.RSTREAM_PROJECT_ID,
  projectEndpoint: process.env.RSTREAM_PROJECT_ENDPOINT,
});
const engine = await c.getEngine();
const { token } = await c.auth.createAuthToken({
  expires_in: 3600,
  resources: {
    tunnels: { scopes: { tunnels: { create: {}, connect: {}, list: {} } } },
  },
});
console.log("RSTREAM_ENGINE=" + engine);
console.log("RSTREAM_AUTHENTICATION_TOKEN=" + token);
