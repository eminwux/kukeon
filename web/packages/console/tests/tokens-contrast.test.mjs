import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

// Issue #1567, item A: muted text must reach WCAG AA (4.5:1) on every surface it sits on, in every theme block.
const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8");

// WCAG 2.x relative luminance and contrast ratio (same helper as terminal-palette.test.mjs).
function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}
function expand(hex) {
  return hex.length === 4 ? `#${[...hex.slice(1)].map(c => c + c).join("")}` : hex;
}

const themes = {
  light: /^:root \{ color-scheme: light;[^}]*\}/m,
  dark: /^:root\[data-theme="dark"\] \{[^}]*\}/m,
  "system dark": /^@media \(prefers-color-scheme: dark\) \{ :root:not\(\[data-theme="light"\]\) \{[^}]*\}/m,
};

function tokens(name) {
  const block = css.match(themes[name]);
  assert.ok(block, `theme block "${name}" not found in style.css`);
  const read = token => {
    const value = block[0].match(new RegExp(`--${token}: (#[0-9a-f]{3,6});`));
    assert.ok(value, `--${token} not set in the ${name} theme`);
    return expand(value[1]);
  };
  return { muted: read("muted"), surfaces: { sidebar: read("sidebar"), panel: read("panel"), canvas: read("canvas"), code: read("code") } };
}

for (const name of Object.keys(themes)) {
  test(`--muted reaches 4.5:1 on sidebar, panel, canvas and code surfaces (${name})`, () => {
    const { muted, surfaces } = tokens(name);
    for (const [surface, background] of Object.entries(surfaces)) {
      const ratio = contrast(muted, background);
      assert.ok(ratio >= 4.5, `${name} --muted ${muted} is ${ratio.toFixed(2)}:1 on --${surface} ${background}`);
    }
  });
}

test("both dark theme blocks agree on the muted and surface tokens", () => {
  assert.deepEqual(tokens("system dark"), tokens("dark"));
});

test("no literal font-size is below 11px, and 11px is only the uppercase eyebrow token", () => {
  assert.doesNotMatch(css, /font-size:\s*(?:[0-9]|10|11)(?:\.\d+)?px/);
  assert.match(css, /--font-xs: 11px;/);
  for (const rule of css.match(/\{[^{}]*var\(--font-xs\)[^{}]*\}/g) ?? []) {
    assert.match(rule, /text-transform: uppercase/, `--font-xs used outside an uppercase eyebrow: ${rule}`);
  }
});
