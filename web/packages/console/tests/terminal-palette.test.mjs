import assert from "node:assert/strict";
import test from "node:test";
import { darkAnsi, darkTerminalBackground, lightAnsi, lightTerminalBackground } from "../src/terminal-palette.js";

// WCAG 2.x relative luminance and contrast ratio.
function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const keys = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
  "brightBlack", "brightRed", "brightGreen", "brightYellow", "brightBlue", "brightMagenta", "brightCyan", "brightWhite"];

test("both palettes define exactly the 16 ANSI colours", () => {
  assert.deepEqual(Object.keys(darkAnsi), keys);
  assert.deepEqual(Object.keys(lightAnsi), keys);
  for (const value of [...Object.values(darkAnsi), ...Object.values(lightAnsi)]) assert.match(value, /^#[0-9a-f]{6}$/);
});

test("every light ANSI colour reaches 3:1 contrast against the light terminal background", () => {
  assert.equal(lightTerminalBackground, "#f8fafc");
  for (const key of keys) {
    const ratio = contrast(lightAnsi[key], lightTerminalBackground);
    assert.ok(ratio >= 3, `${key} ${lightAnsi[key]} is ${ratio.toFixed(2)}:1 against ${lightTerminalBackground}`);
  }
});

test("the light default foreground reaches 4.5:1 against the light terminal background", () => {
  assert.ok(contrast("#20232b", lightTerminalBackground) >= 4.5);
});

test("the dark palette is the one restored in #1401, unchanged", () => {
  assert.equal(darkTerminalBackground, "#0d1421");
  assert.deepEqual(darkAnsi, {
    black: "#000000", red: "#cd0000", green: "#00cd00", yellow: "#cdcd00", blue: "#0000ee", magenta: "#cd00cd", cyan: "#00cdcd", white: "#e5e5e5",
    brightBlack: "#7f7f7f", brightRed: "#ff0000", brightGreen: "#00ff00", brightYellow: "#ffff00", brightBlue: "#5c5cff", brightMagenta: "#ff00ff", brightCyan: "#00ffff", brightWhite: "#ffffff",
  });
});
