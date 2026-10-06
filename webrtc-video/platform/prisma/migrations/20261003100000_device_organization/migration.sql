ALTER TABLE "devices" ALTER COLUMN "userId" DROP NOT NULL;
ALTER TABLE "devices" ADD COLUMN "organizationId" TEXT;
ALTER TABLE "devices" ADD COLUMN "createdById" TEXT;
UPDATE "devices" SET "createdById" = "userId";
ALTER TABLE "devices" ADD CONSTRAINT "devices_exactly_one_owner"
  CHECK (("userId" IS NOT NULL) <> ("organizationId" IS NOT NULL));
CREATE UNIQUE INDEX "devices_organizationId_name_key" ON "devices"("organizationId", "name");
CREATE INDEX "devices_createdById_idx" ON "devices"("createdById");
ALTER TABLE "devices" ADD CONSTRAINT "devices_createdById_fkey"
  FOREIGN KEY ("createdById") REFERENCES "users"("id") ON DELETE SET NULL ON UPDATE CASCADE;
