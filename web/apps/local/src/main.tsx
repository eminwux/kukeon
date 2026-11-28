import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { KukeonConsole } from "@kukeon/console";
import "@kukeon/console/style.css";

// The console kuke serve embeds: every seam at its default (#1752).
const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><KukeonConsole /></StrictMode>);
