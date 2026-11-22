# Deploy the video platform

Return to the [platform overview](../README.md) for the application path. Run commands from `webrtc-video/platform`, unless a step changes directory.

## Deployment profiles

| Goal                                                                     | Platform settings                                                                                                                                                                      |
| ------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Give each user a personal inventory                                      | `DEVICE_ACCESS_MODE=user`, `DEVICE_INVENTORY_MODE=managed` and `VIDEO_DISTRIBUTOR=direct`.                                                                                             |
| Share devices within a GitHub organization and require one device uplink | `DEVICE_ACCESS_MODE=organization`, `VIDEO_DISTRIBUTOR=mediamtx` and `MEDIAMTX_ALLOW_DIRECT_FALLBACK=false`. Choose managed or discovered inventory and configure the adaptive adapter. |

Give each deployment its own database, OAuth application and secrets. Complete the [platform setup](000-setup.md) and provision the application host and HTTPS origin. [Distributor setup](../../distributor/docs/001-setup.md).

## Try the hosted demo

Use the hosted demo as the producer's product backend. Sign in at `https://webrtc-video-platform.demo.rstream.io`, create a device and copy the secret shown by the dashboard. Run these commands from `webrtc-video/platform`.

```bash
cd ../producer
make build-provisioning
API_URL=https://webrtc-video-platform.demo.rstream.io \
DEVICE_SECRET=dev_... \
./webrtc-video-producer -config ./config.provisioning.h264.yaml
```

## Configure demo cleanup

For public demos, `vercel.json` registers a weekly cleanup job.

```json
{
  "crons": [
    {
      "path": "/api/cron/cleanup",
      "schedule": "0 3 * * 0"
    }
  ]
}
```

Set `CRON_SECRET` and `DEMO_CLEANUP_ENABLED="true"` only for disposable demo deployments. Vercel sends the cron secret as a Bearer token in the `Authorization` header when it invokes `/api/cron/cleanup`. Organization mode rejects this cleanup setting. In personal mode the endpoint deletes demo users, accounts, sessions, device records, and verification tokens. It does not touch rstream project configuration.

## Credential boundaries

- Device secrets are product secrets and are stored hashed.
- rstream application credentials stay on the Next.js server.
- Producer tokens are short-lived and allow only tunnel creation for one device tunnel.
- Producer TURN credentials are fetched from the product API when needed.
- Viewer tokens are short-lived and allow only the WHEP resource required by the selected backend.
- Dashboard watch tokens are short-lived and only list tunnels labelled for the current user or configured organization.
- The webhook endpoint accepts only signed rstream lifecycle events and only updates devices carrying this sample's `app` and `device` labels.
- Device creation and TURN credential issuance are bounded to keep the public sample from being used as an unmetered relay minting endpoint.
- The local producer viewer can stay enabled for operator workflows, but the product viewer token does not allow access to `/`.
- Unscoped rstream tokens are intentionally not issued by this sample.
- The demo cleanup cron is disabled by default, protected by `CRON_SECRET`, and should only be enabled for disposable demo databases.
