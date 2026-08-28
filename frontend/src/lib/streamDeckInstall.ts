/** Official Elgato Stream Deck app download page (Windows + macOS). */
export const STREAM_DECK_APP_URL = "https://www.elgato.com/downloads";

/** NEP Commentator plugin bundle served from the commentator frontend origin. */
export const STREAM_DECK_PLUGIN_PATH = "/downloads/com.nep.commentator.streamDeckPlugin";

export function streamDeckPluginURL(origin?: string): string {
  const base = origin?.trim() || (typeof window !== "undefined" ? window.location.origin : "");
  return `${base.replace(/\/$/, "")}${STREAM_DECK_PLUGIN_PATH}`;
}
