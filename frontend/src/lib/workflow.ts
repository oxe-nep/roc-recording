export type ChannelWorkflow = "record" | "tc" | "playout";

export const WORKFLOW_OPTIONS: {
  id: ChannelWorkflow;
  label: string;
  hint: string;
}[] = [
  {
    id: "record",
    label: "Record / Stream",
    hint: "Capture input · REC · SRT",
  },
  {
    id: "tc",
    label: "TC Burn-in",
    hint: "Passthrough with timecode overlay on DeckLink output",
  },
  {
    id: "playout",
    label: "Decode playout",
    hint: "Play SRT or file to DeckLink output",
  },
];

export function normalizeWorkflow(value?: string): ChannelWorkflow {
  if (value === "tc" || value === "playout" || value === "record") return value;
  return "record";
}

export function workflowForChannel(
  workflows: Record<number, ChannelWorkflow>,
  channelId: number,
): ChannelWorkflow {
  return normalizeWorkflow(workflows[channelId]);
}

export function showEncodeCard(
  workflows: Record<number, ChannelWorkflow>,
  channelId: number,
): boolean {
  return workflowForChannel(workflows, channelId) === "record";
}

export function showDecodeCard(
  workflows: Record<number, ChannelWorkflow>,
  channelId: number,
): boolean {
  const w = workflowForChannel(workflows, channelId);
  return w === "playout" || w === "tc";
}

export function workflowLabel(w: ChannelWorkflow): string {
  return WORKFLOW_OPTIONS.find((o) => o.id === w)?.label ?? w;
}
