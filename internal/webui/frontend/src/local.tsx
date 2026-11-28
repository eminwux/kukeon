import { mountConsole } from "./main";

// The console kuke serve embeds: every seam at its default (#1752).
const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
mountConsole(root);
