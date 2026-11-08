import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";

// Execute the packaged binary in an empty directory: no source assets available.
export async function startServer(endpoint) {
  const cwd = await mkdtemp(join(tmpdir(), "kuke-browser-"));
  const binary = resolve(process.env.KUKE_WEB_BINARY ?? "../../../kuke");
  const child = spawn(binary, ["--configuration", "/dev/null", "--host", endpoint ?? `unix://${cwd}/missing.sock`, "serve", "--listen", "127.0.0.1", "--port", "0"], { cwd });
  let output = "";
  const exited = new Promise((resolve) => child.once("close", (code) => resolve(code)));
  async function stop() {
    if (child.exitCode === null) child.kill("SIGINT");
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    try { await exited; } finally { clearTimeout(timer); await rm(cwd, { recursive: true, force: true }); }
  }
  try {
    const url = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`Server startup timeout: ${output}`)), 15000);
      child.once("error", (error) => { clearTimeout(timer); reject(error); });
      child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`Server exited ${code}: ${output}`)); });
      child.stderr.on("data", (data) => { output += data; });
      child.stdout.on("data", (data) => {
        output += data;
        const match = output.match(/Listening on (http:\/\/\S+)/);
        if (match) { clearTimeout(timer); resolve(match[1]); }
      });
    });
    return { url, stop };
  } catch (error) { await stop(); throw error; }
}
