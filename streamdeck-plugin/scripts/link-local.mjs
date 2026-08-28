/**
 * Symlink the local *.sdPlugin folder into Stream Deck's Plugins directory.
 * Run after npm run build. Restart the plugin from Stream Deck afterwards.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));
const pluginSrc = path.resolve(root, "..", "com.nep.commentator.sdPlugin");
const pluginName = "com.nep.commentator.sdPlugin";

function pluginsDir() {
  if (process.platform === "win32") {
    const appData = process.env.APPDATA;
    if (!appData) throw new Error("APPDATA is not set");
    return path.join(appData, "Elgato", "StreamDeck", "Plugins");
  }
  return path.join(os.homedir(), "Library", "Application Support", "com.elgato.StreamDeck", "Plugins");
}

const dest = path.join(pluginsDir(), pluginName);

if (!fs.existsSync(pluginSrc)) {
  console.error(`Missing ${pluginSrc}. Run npm run build first.`);
  process.exit(1);
}

fs.mkdirSync(path.dirname(dest), { recursive: true });

try {
  const stat = fs.lstatSync(dest);
  if (stat.isSymbolicLink() || stat.isDirectory()) {
    fs.rmSync(dest, { recursive: true, force: true });
  } else {
    fs.unlinkSync(dest);
  }
} catch (err) {
  if ((err as NodeJS.ErrnoException).code !== "ENOENT") throw err;
}

const linkType = process.platform === "win32" ? "junction" : "dir";
fs.symlinkSync(pluginSrc, dest, linkType);

console.log(`Linked ${dest}`);
console.log("->", pluginSrc);
console.log("Restart NEP Commentator from Stream Deck → Plugins.");
