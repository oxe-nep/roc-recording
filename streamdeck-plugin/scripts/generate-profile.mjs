/**
 * Generates bundled .streamDeckProfile archives for common Stream Deck models.
 *
 * Page 1: intercom PTT + PGM volume + Connect
 * Page 2: per-intercom volume pairs + back to page 1
 */
import { execSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const PTT_ACTION = "com.nep.commentator.ptt";
const VOLUME_ACTION = "com.nep.commentator.volume";
const CONNECT_ACTION = "com.nep.commentator.connect";
const PAGE_PREV = "com.elgato.streamdeck.page.previous";
const PAGE_NEXT = "com.elgato.streamdeck.page.next";

/** @typedef {{ col: number; row: number; slot: number }} PTTSlot */
/** @typedef {{ col: number; row: number; slot: number; dir: "up" | "down" }} VolSlot */

/**
 * @type {Array<{
 *   file: string;
 *   profileName: string;
 *   model: string;
 *   outerUuid: string;
 *   page1Uuid: string;
 *   page2Uuid: string;
 *   pttSlots: PTTSlot[];
 *   pgmVolume: { down: { col: number; row: number }; up: { col: number; row: number } } | null;
 *   connect?: { col: number; row: number };
 *   connectPage2?: { col: number; row: number };
 *   pageNav: { next: { col: number; row: number }; prev: { col: number; row: number } };
 *   volSlots: VolSlot[];
 * }>}
 */
const PRESETS = [
  {
    file: "commentator.streamDeckProfile",
    profileName: "NEP Commentator",
    model: "20GAA9902",
    outerUuid: "c1a5de5c-d6c9-4e40-aa5b-0c0ffee00001",
    page1Uuid: "a2b3c4d5-e6f7-4890-abcd-0c0ffee00002",
    page2Uuid: "b3c4d5e6-f7a8-4901-bcde-0c0ffee00003",
    // Row 0: five intercom PTT keys
    pttSlots: [
      { col: 0, row: 0, slot: 0 },
      { col: 1, row: 0, slot: 1 },
      { col: 2, row: 0, slot: 2 },
      { col: 3, row: 0, slot: 3 },
      { col: 4, row: 0, slot: 4 },
      // Row 1: sixth intercom + PGM volume
      { col: 0, row: 1, slot: 5 },
    ],
    pgmVolume: { down: { col: 1, row: 1 }, up: { col: 2, row: 1 } },
    connect: { col: 0, row: 2 },
    pageNav: { next: { col: 4, row: 1 }, prev: { col: 4, row: 2 } },
    // Page 2: paired −/+ per intercom, two rows
    volSlots: [
      { col: 0, row: 0, slot: 0, dir: "down" },
      { col: 1, row: 0, slot: 0, dir: "up" },
      { col: 2, row: 0, slot: 1, dir: "down" },
      { col: 3, row: 0, slot: 1, dir: "up" },
      { col: 4, row: 0, slot: 2, dir: "down" },
      { col: 0, row: 1, slot: 2, dir: "up" },
      { col: 1, row: 1, slot: 3, dir: "down" },
      { col: 2, row: 1, slot: 3, dir: "up" },
      { col: 3, row: 1, slot: 4, dir: "down" },
      { col: 4, row: 1, slot: 4, dir: "up" },
      { col: 0, row: 2, slot: 5, dir: "down" },
      { col: 1, row: 2, slot: 5, dir: "up" },
    ],
  },
  {
    file: "commentator-xl.streamDeckProfile",
    profileName: "NEP Commentator XL",
    model: "20GBA9901",
    outerUuid: "c1a5de5c-d6c9-4e40-aa5b-0c0ffee00011",
    page1Uuid: "a2b3c4d5-e6f7-4890-abcd-0c0ffee00012",
    page2Uuid: "b3c4d5e6-f7a8-4901-bcde-0c0ffee00013",
    // Row 0: six intercom PTT + PGM on the right
    pttSlots: [
      { col: 0, row: 0, slot: 0 },
      { col: 1, row: 0, slot: 1 },
      { col: 2, row: 0, slot: 2 },
      { col: 3, row: 0, slot: 3 },
      { col: 4, row: 0, slot: 4 },
      { col: 5, row: 0, slot: 5 },
    ],
    pgmVolume: { down: { col: 6, row: 0 }, up: { col: 7, row: 0 } },
    connect: { col: 0, row: 1 },
    pageNav: { next: { col: 7, row: 1 }, prev: { col: 7, row: 2 } },
    // Page 2: four intercom pairs on row 0, two on row 1
    volSlots: [
      { col: 0, row: 0, slot: 0, dir: "down" },
      { col: 1, row: 0, slot: 0, dir: "up" },
      { col: 2, row: 0, slot: 1, dir: "down" },
      { col: 3, row: 0, slot: 1, dir: "up" },
      { col: 4, row: 0, slot: 2, dir: "down" },
      { col: 5, row: 0, slot: 2, dir: "up" },
      { col: 6, row: 0, slot: 3, dir: "down" },
      { col: 7, row: 0, slot: 3, dir: "up" },
      { col: 0, row: 1, slot: 4, dir: "down" },
      { col: 1, row: 1, slot: 4, dir: "up" },
      { col: 2, row: 1, slot: 5, dir: "down" },
      { col: 3, row: 1, slot: 5, dir: "up" },
    ],
  },
  {
    file: "commentator-mini.streamDeckProfile",
    profileName: "NEP Commentator Mini",
    model: "20GAM9901",
    outerUuid: "c1a5de5c-d6c9-4e40-aa5b-0c0ffee00021",
    page1Uuid: "a2b3c4d5-e6f7-4890-abcd-0c0ffee00022",
    page2Uuid: "b3c4d5e6-f7a8-4901-bcde-0c0ffee00023",
    // All six intercom PTT keys on page 1
    pttSlots: [
      { col: 0, row: 0, slot: 0 },
      { col: 1, row: 0, slot: 1 },
      { col: 2, row: 0, slot: 2 },
      { col: 0, row: 1, slot: 3 },
      { col: 1, row: 1, slot: 4 },
      { col: 2, row: 1, slot: 5 },
    ],
    pgmVolume: null,
    pageNav: { next: { col: 2, row: 0 }, prev: { col: 2, row: 1 } },
    volSlots: [],
    pgmVolumePage2: { down: { col: 0, row: 0 }, up: { col: 1, row: 0 } },
    connectPage2: { col: 2, row: 0 },
    volSlotsPage2: [
      { col: 0, row: 1, slot: 0, dir: "down" },
      { col: 1, row: 1, slot: 0, dir: "up" },
    ],
  },
];

function pluginAction(uuid, name, settings) {
  return {
    ActionID: randomUUID(),
    LinkedTitle: false,
    Name: name,
    Resources: null,
    Settings: settings,
    State: 0,
    States: [{}],
    UUID: uuid,
  };
}

function systemAction(uuid, name, title) {
  return {
    ActionID: randomUUID(),
    LinkedTitle: true,
    Name: name,
    Resources: null,
    Settings: {},
    State: 0,
    States: [{ Title: title, ShowTitle: true, TitleAlignment: "bottom", FontSize: 11 }],
    UUID: uuid,
  };
}

function key(actions, col, row, entry) {
  actions[`${col},${row}`] = entry;
}

function addPgmVolume(actions, pgmVolume) {
  if (!pgmVolume) return;
  const { down, up } = pgmVolume;
  key(actions, down.col, down.row, pluginAction(VOLUME_ACTION, "PGM Volume", { target: "pgm", direction: "down" }));
  key(actions, up.col, up.row, pluginAction(VOLUME_ACTION, "PGM Volume", { target: "pgm", direction: "up" }));
}

function buildPage1(preset) {
  const actions = {};
  for (const { col, row, slot } of preset.pttSlots) {
    key(actions, col, row, pluginAction(PTT_ACTION, "Intercom PTT", { slot }));
  }
  addPgmVolume(actions, preset.pgmVolume);
  if (preset.connect) {
    key(actions, preset.connect.col, preset.connect.row, pluginAction(CONNECT_ACTION, "Connect", {}));
  }
  key(actions, preset.pageNav.next.col, preset.pageNav.next.row, systemAction(PAGE_NEXT, "Next Page", "Vol →"));
  return actions;
}

function buildPage2(preset) {
  const actions = {};
  const volSlots = preset.volSlotsPage2 ?? preset.volSlots;
  for (const { col, row, slot, dir } of volSlots) {
    key(
      actions,
      col,
      row,
      pluginAction(VOLUME_ACTION, "Intercom Volume", { target: "intercom", slot, direction: dir }),
    );
  }
  addPgmVolume(actions, preset.pgmVolumePage2);
  if (preset.connectPage2) {
    key(actions, preset.connectPage2.col, preset.connectPage2.row, pluginAction(CONNECT_ACTION, "Connect", {}));
  }
  key(actions, preset.pageNav.prev.col, preset.pageNav.prev.row, systemAction(PAGE_PREV, "Previous Page", "← PTT"));
  return actions;
}

function zipDir(sourceDir, outputPath) {
  const out = resolve(outputPath);
  mkdirSync(dirname(out), { recursive: true });
  try {
    rmSync(out, { force: true });
  } catch {
    /* missing */
  }
  if (process.platform === "win32") {
    const src = sourceDir.replace(/'/g, "''");
    const zipPath = `${out}.zip`;
    try {
      rmSync(zipPath, { force: true });
    } catch {
      /* missing */
    }
    execSync(
      `powershell -NoProfile -Command "Compress-Archive -Path '${src}\\*' -DestinationPath '${zipPath.replace(/'/g, "''")}' -Force"`,
      { stdio: "inherit" },
    );
    execSync(
      `powershell -NoProfile -Command "Move-Item -Path '${zipPath.replace(/'/g, "''")}' -Destination '${out.replace(/'/g, "''")}' -Force"`,
      { stdio: "inherit" },
    );
  } else {
    execSync(`cd "${sourceDir}" && zip -qr "${out}" . -x ".*"`, { stdio: "inherit" });
  }
}

export function writeProfileArchive(outputPath, preset) {
  const staging = mkdtempSync(join(tmpdir(), "nep-commentator-profile-"));
  try {
    const sdProfileDir = `${preset.outerUuid.toUpperCase()}.sdProfile`;
    const base = join(staging, sdProfileDir);
    const page1Dir = join(base, "Profiles", preset.page1Uuid.toUpperCase());
    const page2Dir = join(base, "Profiles", preset.page2Uuid.toUpperCase());
    mkdirSync(page1Dir, { recursive: true });
    mkdirSync(page2Dir, { recursive: true });

    writeFileSync(
      join(base, "manifest.json"),
      JSON.stringify({
        AppIdentifier: "*",
        Device: { Model: preset.model, UUID: "" },
        Name: preset.profileName,
        Pages: {
          Current: preset.page1Uuid,
          Default: preset.page1Uuid,
          Pages: [preset.page1Uuid, preset.page2Uuid],
        },
        Version: "3.0",
      }),
    );
    writeFileSync(
      join(page1Dir, "manifest.json"),
      JSON.stringify({
        Controllers: [{ Actions: buildPage1(preset), Type: "Keypad" }],
        Icon: "",
        Name: "Intercom",
      }),
    );
    writeFileSync(
      join(page2Dir, "manifest.json"),
      JSON.stringify({
        Controllers: [{ Actions: buildPage2(preset), Type: "Keypad" }],
        Icon: "",
        Name: "Volume",
      }),
    );
    zipDir(staging, resolve(outputPath));
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

const scriptDir = dirname(fileURLToPath(import.meta.url));
const profilesDir = resolve(scriptDir, "..", "com.nep.commentator.sdPlugin", "profiles");
mkdirSync(profilesDir, { recursive: true });
for (const preset of PRESETS) {
  const target = join(profilesDir, preset.file);
  writeProfileArchive(target, preset);
  console.log(`wrote ${target}`);
}
