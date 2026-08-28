import streamDeck from "@elgato/streamdeck";

/** Manifest Profiles[].Name — path without extension. */
export const COMMENTATOR_PROFILE = "profiles/commentator";

export async function activateCommentatorProfile(): Promise<void> {
  const tasks: Promise<void>[] = [];
  for (const device of streamDeck.devices) {
    tasks.push(
      streamDeck.profiles.switchToProfile(device.id, COMMENTATOR_PROFILE).catch((err) => {
        console.error(`[nep-commentator] switch profile on ${device.id}:`, err);
      }),
    );
  }
  await Promise.all(tasks);
}
