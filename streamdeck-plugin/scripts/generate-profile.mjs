/**
 * Generates profiles/commentator.streamDeckProfile:
 * - Page 1: intercom PTT (6 keys) + PGM volume
 * - Page 2: per-intercom volume +/- (6 intercom slots)
 */
import { execSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";

const PTT_ACTION = "com.nep.commentator.ptt";
const VOLUME_ACTION = "com.nep.commentator.volume";
const PAGE_PREV = "com.elgato.streamdeck.page.previous";
const PAGE_NEXT = "com.elgato.streamdeck.page.next";
const NULL_UUID = "00000000-0000-0000-0000-000000000000";
const OUTER_UUID = "c1a5de5c-d6c9-4e40-aa5b-0c0ffee00001";
const PAGE1_UUID = "a2b3c4d5-e6f7-4890-abcd-0c0ffee00002";
const PAGE2_UUID = "b3c4d5e6-f7a8-4901-bcde-0c0ffee00003";
const MK2_MODEL = "20GAA9902";

function actionEntry(uuid, name, settings, title) {
  return {
    ActionID: NULL_UUID,
    LinkedTitle: false,
    Name: name,
    Resources: null,
    Settings: settings,
    State: 0,
    States: [{ Title: title, ShowTitle: true, TitleAlignment: "bottom", FontSize: 11 }],
    UUID: uuid,
  };
}

function buildPage1Actions() {
  const actions = {};
  const pttSlots = [
    { col: 0, row: 0, slot: 0 },
    { col: 1, row: 0, slot: 1 },
    { col: 2, row: 0, slot: 2 },
    { col: 3, row: 0, slot: 3 },
    { col: 4, row: 0, slot: 4 },
    { col: 0, row: 1, slot: 5 },
  ];
  for (const { col, row, slot } of pttSlots) {
    actions[`${col},${row}`] = actionEntry(PTT_ACTION, "Intercom PTT", { slot }, "PTT");
  }
  actions["1,1"] = actionEntry(VOLUME_ACTION, "PGM Volume", { target: "pgm", direction: "down" }, "PGM −");
  actions["2,1"] = actionEntry(VOLUME_ACTION, "PGM Volume", { target: "pgm", direction: "up" }, "PGM +");
  actions["3,1"] = actionEntry(PAGE_NEXT, "Next Page", {}, "Vol →");
  actions["4,2"] = actionEntry(PAGE_PREV, "Previous Page", {}, "← PTT");
  return actions;
}

function buildPage2Actions() {
  const actions = {};
  const volSlots = [
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
  ];
  for (const { col, row, slot, dir } of volSlots) {
    const sign = dir === "up" ? "+" : "−";
    actions[`${col},${row}`] = actionEntry(
      VOLUME_ACTION,
      "Intercom Volume",
      { target: "intercom", slot, direction: dir },
      `IC${slot + 1} ${sign}`,
    );
  }
  actions["4,2"] = actionEntry(PAGE_PREV, "Previous Page", {}, "← PTT");
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
    execSync(`powershell -NoProfile -Command "Move-Item -Path '${zipPath.replace(/'/g, "''")}' -Destination '${out.replace(/'/g, "''")}' -Force"`, {
      stdio: "inherit",
    });
  } else {
    execSync(`cd "${sourceDir}" && zip -qr "${out}" . -x ".*"`, { stdio: "inherit" });
  }
}

export function writeProfileArchive(outputPath, profileName = "NEP Commentator") {
  const staging = mkdtempSync(join(tmpdir(), "nep-commentator-profile-"));
  try {
    const sdProfileDir = `${OUTER_UUID.toUpperCase()}.sdProfile`;
    const base = join(staging, sdProfileDir);
    const page1Dir = join(base, "Profiles", PAGE1_UUID.toUpperCase());
    const page2Dir = join(base, "Profiles", PAGE2_UUID.toUpperCase());
    mkdirSync(page1Dir, { recursive: true });
    mkdirSync(page2Dir, { recursive: true });

    writeFileSync(
      join(base, "manifest.json"),
      JSON.stringify({
        AppIdentifier: "*",
        Device: { Model: MK2_MODEL, UUID: "" },
        Name: profileName,
        Pages: {
          Current: PAGE1_UUID,
          Default: PAGE1_UUID,
          Pages: [PAGE1_UUID, PAGE2_UUID],
        },
        Version: "3.0",
      }),
    );
    writeFileSync(
      join(page1Dir, "manifest.json"),
      JSON.stringify({
        Controllers: [{ Actions: buildPage1Actions(), Type: "Keypad" }],
        Icon: "",
        Name: "PTT",
      }),
    );
    writeFileSync(
      join(page2Dir, "manifest.json"),
      JSON.stringify({
        Controllers: [{ Actions: buildPage2Actions(), Type: "Keypad" }],
        Icon: "",
        Name: "Volumes",
      }),
    );
    zipDir(staging, resolve(outputPath));
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

const scriptDir = dirname(fileURLToPath(import.meta.url));
const target = resolve(scriptDir, "..", "com.nep.commentator.sdPlugin", "profiles", "commentator.streamDeckProfile");
writeProfileArchive(target);
console.log(`wrote ${target}`);
