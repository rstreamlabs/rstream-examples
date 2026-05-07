WITH ranked_devices AS (
  SELECT
    id,
    row_number() OVER (
      PARTITION BY "userId", name
      ORDER BY "createdAt", id
    ) AS rank
  FROM "devices"
)
UPDATE "devices"
SET name = left("devices".name, 67) || ' ' || left("devices".id, 8)
FROM ranked_devices
WHERE "devices".id = ranked_devices.id
  AND ranked_devices.rank > 1;

CREATE UNIQUE INDEX "devices_userId_name_key" ON "devices"("userId", "name");
