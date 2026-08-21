import { turnCredentialsSchema } from "@rstreamlabs/rstream/turn";
import { z } from "zod";

export const expiringTurnCredentialsSchema = turnCredentialsSchema.extend({
  expiresAt: z.string().datetime({ offset: true }),
});
