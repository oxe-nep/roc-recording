export type ChannelWorkflowMode = "pair" | "encode" | "decode" | "tc";

export interface ChannelWorkflowConfig {
  mode: ChannelWorkflowMode;
}

export const DEFAULT_WORKFLOW: ChannelWorkflowConfig = { mode: "pair" };

export const WORKFLOW_OPTIONS: {
  mode: ChannelWorkflowMode;
  label: string;
  hint: string;
}[] = [
  { mode: "pair", label: "I/O", hint: "Encode + decode" },
  { mode: "encode", label: "In", hint: "Record / ingest" },
  { mode: "decode", label: "Out", hint: "Playout" },
  { mode: "tc", label: "TC", hint: "Timecode burn-in" },
];

const VALID_MODES = new Set<ChannelWorkflowMode>(["pair", "encode", "decode", "tc"]);

export function normalizeWorkflowConfig(
  value?: Partial<ChannelWorkflowConfig> | { encode?: boolean; decode?: boolean } | string,
): ChannelWorkflowConfig {
  if (typeof value === "string") {
    if (value === "tc") return { mode: "tc" };
    if (value === "record" || value === "encode") return { mode: "encode" };
    if (value === "playout" || value === "decode") return { mode: "decode" };
    return { ...DEFAULT_WORKFLOW };
  }
  if (value && "mode" in value && value.mode && VALID_MODES.has(value.mode)) {
    return { mode: value.mode };
  }
  if (value && ("encode" in value || "decode" in value)) {
    const encode = value.encode !== false;
    const decode = value.decode !== false;
    if (encode && decode) return { mode: "pair" };
    if (encode) return { mode: "encode" };
    if (decode) return { mode: "decode" };
    return { ...DEFAULT_WORKFLOW };
  }
  return { ...DEFAULT_WORKFLOW };
}

export function workflowForChannel(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): ChannelWorkflowConfig {
  return normalizeWorkflowConfig(workflows[channelId]);
}

export function workflowMode(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): ChannelWorkflowMode {
  return workflowForChannel(workflows, channelId).mode;
}

export function showEncodeCard(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): boolean {
  const mode = workflowMode(workflows, channelId);
  return mode === "pair" || mode === "encode";
}

export function showDecodeCard(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): boolean {
  const mode = workflowMode(workflows, channelId);
  return mode === "pair" || mode === "decode";
}

export function showTcCard(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): boolean {
  return workflowMode(workflows, channelId) === "tc";
}

export function isTcWorkflow(
  workflows: Record<number, ChannelWorkflowConfig>,
  channelId: number,
): boolean {
  return workflowMode(workflows, channelId) === "tc";
}
