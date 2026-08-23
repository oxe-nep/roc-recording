export interface ChannelWorkflowConfig {
  encode: boolean;
  decode: boolean;
}

export const DEFAULT_WORKFLOW: ChannelWorkflowConfig = { encode: true, decode: true };

export function normalizeWorkflowConfig(value?: Partial<ChannelWorkflowConfig> | string): ChannelWorkflowConfig {
  if (typeof value === "string") {
    switch (value) {
      case "playout":
        return { encode: false, decode: true };
      case "record":
        return { encode: true, decode: false };
      default:
        return { ...DEFAULT_WORKFLOW };
    }
  }
  const encode = value?.encode !== false;
  const decode = value?.decode !== false;
  if (!encode && !decode) return { ...DEFAULT_WORKFLOW };
  return { encode, decode };
}

export function workflowForChannel(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): ChannelWorkflowConfig {
  return normalizeWorkflowConfig(workflows[channelId]);
}

export function showEncodeCard(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): boolean {
  return workflowForChannel(workflows, channelId).encode;
}

export function showDecodeCard(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): boolean {
  return workflowForChannel(workflows, channelId).decode;
}
