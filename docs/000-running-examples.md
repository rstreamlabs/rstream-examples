# Run an rstream example

From the repository checkout, open the application directory listed in
the [example catalog](../README.md). Follow its prerequisites, run
commands and expected result. The [Next.js preview](../nextjs-rstream-preview/README.md)
has a loopback path you can try before configuring rstream.

## Choose how the example connects

| Where it runs | What to configure |
| --- | --- |
| Hosted project | Install the CLI and select the project that permits the example's tunnel type. Give unattended services their own scoped identity. |
| Self-hosted Engine | Choose an example that supports the Engine's capabilities. Supply a direct Engine context, the agent credential and its CA bundle when required. |
| Application-managed device | Follow the example's provisioning flow. The backend issues scoped runtime configuration; the device authenticates to that backend with its application identity. |
| Local test fixture | Follow the example's verification command. Unit and local fixture checks run independently of a hosted project. |

To select a hosted project, [install the CLI](https://rstream.io/docs/getting-started/installation)
and use the endpoint shown by `rstream project list`. If the list is empty, create a project in the [Dashboard](https://rstream.io/dashboard) or ask for access to an existing one.

```sh
rstream login
```

List the projects available to your account.

```sh
rstream project list
```

Select the endpoint from the list.

```sh
rstream project use my-project-endpoint
```

For a direct Engine, follow the [self-hosted agent setup](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context).
When the Engine uses a private CA, configure trust both for the agent
runtime and for any client that opens a published HTTPS address.

## Verify the result and stop the example

A published tunnel normally reports a URL or host and port. Use the
verification command in the sample with the access method the project
requires. For a private tunnel, start the owner before its dial client or
local relay. Use the same project or reachable Engine on both sides.

Stop example processes in the order shown in their guides and remove any
test credentials you provisioned. Use [Testing](001-testing.md) when you
need unit tests, a repeatable runtime check or a load scenario.
