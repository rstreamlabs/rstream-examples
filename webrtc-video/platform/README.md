# WebRTC video platform

This Next.js application adds device inventory, GitHub sign-in and viewer authorization to the [Go video producer](../producer/README.md). The application provisions scoped rstream tunnel and TURN credentials, so devices and browser users do not need a CLI login or a long-lived rstream token.

The producer keeps ownership of capture and media adaptation. The platform owns product identity and access; it does not proxy live media. Direct delivery serves one producer-to-browser session. The optional [MediaMTX backend](../distributor/README.md) shares one device upstream among several viewers.

## Run the application

Use Node.js 24, npm, PostgreSQL, a GitHub OAuth application and rstream application credentials for the target project. Run commands from this directory.

```sh
cp .env.example .env.local
```

Fill the database URLs, GitHub callback credentials, `NEXTAUTH_SECRET`, `RSTREAM_CLIENT_ID`, `RSTREAM_CLIENT_SECRET` and `RSTREAM_PROJECT_ENDPOINT` before continuing. [Platform setup](docs/000-setup.md) explains permissions, callbacks and optional settings. Use a database dedicated to this sample.

```sh
npm ci
npm run prisma:deploy
npm run dev
```

Open `http://localhost:3000`, sign in with GitHub and create a device. Copy its generated device secret for the producer.

In a second terminal, starting from this directory:

```sh
cd ../producer
make build-provisioning
API_URL=http://localhost:3000 \
DEVICE_SECRET=dev_... \
./webrtc-video-producer -config ./config.provisioning.h264.yaml
```

Replace `dev_...` with that device’s secret. The device appears online in the dashboard; opening it starts the authorized WHEP viewer. The no-viewer producer build does not require Node.js on the device.

## Choose the application model

| Choice    | Default                                                         | Alternative                                                                           |
| --------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Access    | Each GitHub user owns an inventory.                             | Organization members share devices.                                                   |
| Inventory | The platform registers devices and issues provisioning secrets. | An organization discovers stable, labeled devices using existing project credentials. |
| Delivery  | Direct WebRTC to the producer.                                  | A MediaMTX adapter shares one upstream for several viewers.                           |

These choices have distinct authorization and lifecycle rules. Configure them through [Platform setup](docs/000-setup.md), including whether direct fallback is allowed when the distributor is unavailable.

## Documentation

| Task                                                          | Guide                                                                                                           |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Understand provisioning, tokens and the SDK boundaries        | [Architecture](docs/001-architecture.md)                                                                        |
| Configure access, inventory, project credentials and delivery | [Setup](docs/000-setup.md)                                                                                      |
| Add distribution metrics, recordings or source controls       | [Media capabilities](docs/002-media.md)                                                                         |
| Configure exposure, retention and deployment secrets          | [Deployment](docs/004-deployment.md)                                                                            |
| Validate application changes and media integration            | [Testing](docs/003-testing.md)                                                                                  |
| Follow the product integration walkthrough                    | [Next.js guide](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream) |

See [Contributing](../../CONTRIBUTING.md) and [Security](../../SECURITY.md) for repository-wide guidance.
