import type { UIMessage } from "ai";

export interface MeshMetadata {
  worker: string;
}

export type MeshUIMessage = UIMessage<MeshMetadata>;
