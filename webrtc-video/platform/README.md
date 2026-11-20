# WebRTC video platform

Publish a video device through rstream and let its owner watch from a signed-in Next.js dashboard. The platform issues scoped tunnel, TURN and viewer credentials. The Go producer and browser establish the live media path.

## Run the application

Use Node.js 24 with npm, a dedicated PostgreSQL database and a GitHub OAuth application with callback `http://localhost:3000/api/auth/callback/github`. Use a hosted project that permits token-authenticated HTTP publication and managed TURN. Create a server-side [application credential with the required project permissions](docs/000-setup.md#rstream-project-setup). The producer needs the [Go and GStreamer toolchain](../producer/docs/000-build.md#requirements).

Create the platform environment file from the template in `webrtc-video/platform`.

```sh
cp .env.example .env.local
```

Fill the database URLs, GitHub OAuth credentials, `NEXTAUTH_SECRET`, `RSTREAM_CLIENT_ID`, `RSTREAM_CLIENT_SECRET` and `RSTREAM_PROJECT_ENDPOINT` using the [environment table](docs/000-setup.md). Keep `NEXTAUTH_URL` at `http://localhost:3000` and `VIDEO_DISTRIBUTOR` at `direct` for this run.

```sh
npm ci
npm run prisma:deploy
npm run dev
```

Open `http://localhost:3000`, sign in with GitHub and add a device. Copy the secret shown when the device is created. Open a second terminal in `webrtc-video/platform` to build and run the provisioned producer on the same machine.

```sh
cd ../producer
make build-provisioning
API_URL=http://localhost:3000 \
DEVICE_SECRET='replace-with-the-device-secret' \
./webrtc-video-producer -config ./config.provisioning.h264.yaml
```

The device appears online. Select it in the dashboard to play the H.264 test pattern. Stop the producer and platform with `Ctrl-C` when finished. The provisioning build on a separate device uses Go and GStreamer and needs a reachable platform URL as `API_URL`.

## Choose access, inventory and delivery

| Choice    | Default                                                         | Other supported mode                                                                              |
| --------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| Access    | Every GitHub user owns a private inventory.                     | Verified organization members share devices.                                                      |
| Inventory | The platform registers devices and issues provisioning secrets. | An organization discovers stable, labeled tunnels published with existing project credentials.    |
| Delivery  | Direct WebRTC from the producer.                                | [MediaMTX distribution](../distributor/README.md) shares one device uplink among several viewers. |

Use [Platform setup](docs/000-setup.md) for organization access, discovery, webhook delivery and MediaMTX exposure. See [Media capabilities](docs/002-media.md) for distribution metrics, recent recordings and source-quality controls.

## Build and verify the platform

| Task                                                | Guide                                             |
| --------------------------------------------------- | ------------------------------------------------- |
| Map provisioning, access and media boundaries       | [Platform architecture](docs/001-architecture.md) |
| Configure production exposure, database and secrets | [Deployment](docs/004-deployment.md)              |
| Run local checks and live integration profiles      | [Testing](docs/003-testing.md)                    |

Follow the [Next.js walkthrough](https://rstream.io/guides/integrate-webrtc-video-streaming-into-a-nextjs-platform-with-rstream) for the platform and SDK integration in context.
