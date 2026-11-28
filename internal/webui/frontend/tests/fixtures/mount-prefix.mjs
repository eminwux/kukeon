// The console mounted with a non-default transport (#1752): mount.spec.mjs builds this page and checks
// that every API request and the attach WebSocket use the /x/api prefix.
import { mountConsole } from "../../src/main.tsx";

mountConsole(document.getElementById("root"), { transport: { apiBase: "/x/api" } });
