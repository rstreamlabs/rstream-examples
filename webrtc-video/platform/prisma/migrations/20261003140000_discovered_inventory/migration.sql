CREATE TABLE "discovered_devices" (
  "projectId" TEXT NOT NULL,
  "deviceId" TEXT NOT NULL,
  "name" TEXT NOT NULL,
  "tunnelName" TEXT NOT NULL,
  "firstSeenAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "lastSeenAt" TIMESTAMP(3) NOT NULL,
  CONSTRAINT "discovered_devices_pkey" PRIMARY KEY ("projectId", "deviceId")
);
CREATE INDEX "discovered_devices_projectId_lastSeenAt_idx" ON "discovered_devices"("projectId", "lastSeenAt");
