// One-off generator for the PWA manifest's PNG icons, rasterized from the
// existing public/favicon.svg mark. Not part of the build — run manually
// (`node scripts/generate-icons.mjs`) whenever the source mark changes.
// Requires `sharp` (installed transiently with --no-save; it's a build-time
// image tool, not a runtime dependency of the app).
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const SOURCE = fileURLToPath(new URL("../public/favicon.svg", import.meta.url));
const OUT_DIR = fileURLToPath(new URL("../public/icons/", import.meta.url));

await mkdir(OUT_DIR, { recursive: true });

// "any" purpose: transparent background, the mark centered and padded to
// fill the square — safe for browsers/OSes that render the icon as-is.
for (const size of [192, 512]) {
  await sharp(SOURCE, { density: 384 })
    .resize(size, size, { fit: "contain", background: { r: 0, g: 0, b: 0, alpha: 0 } })
    .png()
    .toFile(`${OUT_DIR}icon-${size}.png`);
}

// "maskable" purpose: OSes crop this to their own shape (circle, squircle,
// etc.), so per the maskable-icon spec the artwork must sit inside the
// middle ~80% "safe zone" on an opaque background — a transparent maskable
// icon would show as blank wherever the OS's mask doesn't cover a pixel.
const maskableSize = 512;
const markSize = Math.round(maskableSize * 0.6);
const mark = await sharp(SOURCE, { density: 384 })
  .resize(markSize, markSize, { fit: "contain", background: { r: 0, g: 0, b: 0, alpha: 0 } })
  .png()
  .toBuffer();

await sharp({
  create: { width: maskableSize, height: maskableSize, channels: 4, background: "#ffffff" },
})
  .composite([{ input: mark, gravity: "center" }])
  .png()
  .toFile(`${OUT_DIR}icon-512-maskable.png`);

console.log("Generated icon-192.png, icon-512.png, icon-512-maskable.png in public/icons/");
