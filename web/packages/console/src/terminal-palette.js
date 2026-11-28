// ANSI palettes for the terminal (issue #1566). Each app theme gets 16 colours
// readable on its own terminal background. darkAnsi is the palette restored in
// #1401 and must stay unchanged; lightAnsi is tuned for lightTerminalBackground,
// every colour at 3:1 contrast or more (tests/terminal-palette.test.mjs).
export const darkTerminalBackground = "#0d1421";
export const lightTerminalBackground = "#f8fafc";

export const darkAnsi = {
  black: "#000000", red: "#cd0000", green: "#00cd00", yellow: "#cdcd00", blue: "#0000ee", magenta: "#cd00cd", cyan: "#00cdcd", white: "#e5e5e5",
  brightBlack: "#7f7f7f", brightRed: "#ff0000", brightGreen: "#00ff00", brightYellow: "#ffff00", brightBlue: "#5c5cff", brightMagenta: "#ff00ff", brightCyan: "#00ffff", brightWhite: "#ffffff",
};

// White and bright white become mid greys and yellow a dark amber, so prompts,
// `ls --color` and TUIs stay readable on the light background.
export const lightAnsi = {
  black: "#1f2328", red: "#b3261e", green: "#1a7f37", yellow: "#8a6100", blue: "#0b57d0", magenta: "#8e24aa", cyan: "#00748a", white: "#6e7781",
  brightBlack: "#57606a", brightRed: "#d1242f", brightGreen: "#1f883d", brightYellow: "#9a6700", brightBlue: "#0969da", brightMagenta: "#a626a4", brightCyan: "#0e7d8f", brightWhite: "#808891",
};
