import streamDeck, { DeviceType } from "@elgato/streamdeck";

/** Manifest Profiles[].Name — path without extension. */
export const PROFILES: Partial<Record<DeviceType, string>> = {
  [DeviceType.StreamDeck]: "profiles/commentator",
  [DeviceType.StreamDeckMini]: "profiles/commentator-mini",
  [DeviceType.StreamDeckXL]: "profiles/commentator-xl",
};

const DEFAULT_PROFILE = "profiles/commentator";

export function profileForDevice(type: DeviceType): string {
  return PROFILES[type] ?? DEFAULT_PROFILE;
}

export async function activateCommentatorProfile(deviceId?: string): Promise<boolean> {
  const targets = deviceId
    ? streamDeck.devices.filter((device) => device.id === deviceId)
    : [...streamDeck.devices];

  if (targets.length === 0) return false;

  let switched = false;
  for (const device of targets) {
    const profile = profileForDevice(device.type);
    try {
      await streamDeck.profiles.switchToProfile(device.id, profile);
      switched = true;
      console.log(`[nep-commentator] switched ${device.id} to ${profile}`);
    } catch (err) {
      console.error(`[nep-commentator] switch profile (${profile}) on ${device.id}:`, err);
    }
  }
  return switched;
}

/** Stream Deck may not have devices ready at pair time — retry a few times. */
export function scheduleProfileActivation(deviceId?: string): void {
  const run = () => {
    void activateCommentatorProfile(deviceId);
  };
  run();
  setTimeout(run, 1000);
  setTimeout(run, 3000);
}
