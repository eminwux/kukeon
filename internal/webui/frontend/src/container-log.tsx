import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { ErrorNotice, toFailure, type Failure } from "./request";
import { apiPath } from "./routes";
import { terminalThemeFor, type Theme } from "./terminal";
import { apiFetch } from "./transport";

// One chunk of GET …/containers/{name}/log (issue #1620). data is base64 of the raw stream bytes.
type LogChunk = { source?: string; data?: string | null; start?: number; next?: number; size?: number; truncated?: boolean; more?: boolean };
const TAIL_BYTES = 256 * 1024;
const FOLLOW_INTERVAL_MS = 2000;
const CHUNKS_PER_SYNC = 8;

// null means the container has no stream file yet (404 log_unavailable).
async function readChunk(scope: string[], container: string, offset: number, signal: AbortSignal): Promise<LogChunk | null> {
  const url = `${apiPath(scope)}/containers/${encodeURIComponent(container)}/log?offset=${offset}&maxBytes=${TAIL_BYTES}`;
  const response = await apiFetch(url, { signal, headers: { Accept: "application/json" } });
  let body: { error?: Failure } & LogChunk = {};
  try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
  if (response.status === 404 && body.error?.code === "log_unavailable") return null;
  if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
  return body;
}

// Captures are raw terminal bytes and may not be valid UTF-8: decode to bytes, never to a string.
const decode = (data?: string | null) => data ? Uint8Array.from(atob(data), ch => ch.charCodeAt(0)) : new Uint8Array();

// A read-only view of a container's output stream, shown in the terminal area when no live terminal is
// available: a non-attachable container, or any container of a dead cell. It never opens a socket and
// never sends anything to the container, so a resize here cannot reach its process.
export function ContainerLog({ scope, container, attachable, live, cellState, revision, theme, expanded }: { scope: string[]; container: string; attachable: boolean; live: boolean; cellState?: string; revision: number; theme: Theme; expanded: boolean }) {
  const mount = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal | null>(null);
  const fitNow = useRef<() => void>(() => {});
  const controller = useRef<AbortController | null>(null);
  const cursor = useRef<number | null>(null);
  const busy = useRef(false);
  const again = useRef(false);
  const [empty, setEmpty] = useState(false);
  const [omitted, setOmitted] = useState(false);
  const [error, setError] = useState<Failure>();

  // One read routine. With no cursor it resets the screen and reads the tail; with a cursor it only reads
  // forward, so a re-run (refresh, lifecycle action, state change) never loses what is on screen.
  async function sync(signal: AbortSignal) {
    // A trigger that arrives mid-read is not dropped: it reruns once the current read ends.
    if (busy.current) { again.current = true; return; }
    busy.current = true;
    try {
      for (let i = 0; i < CHUNKS_PER_SYNC; i++) {
        const tail = cursor.current === null;
        const chunk = await readChunk(scope, container, tail ? -1 : cursor.current!, signal);
        if (signal.aborted) return;
        setError(undefined);
        if (chunk === null) { setEmpty(true); return; }
        // The file is smaller than the cursor: truncated, replaced or rotated. Start over from the tail.
        if (!tail && (chunk.size ?? 0) < cursor.current!) { cursor.current = null; continue; }
        if (tail) { term.current?.reset(); setOmitted(!!chunk.truncated); }
        const bytes = decode(chunk.data);
        if (bytes.length) term.current?.write(bytes);
        cursor.current = chunk.next ?? cursor.current;
        setEmpty((chunk.size ?? 0) === 0);
        if (!chunk.more) return;
      }
    } catch (failure) {
      if (!signal.aborted) setError(toFailure(failure));
    } finally {
      busy.current = false;
      // Rerun on the live controller: a read aborted by a remount must not swallow the new xterm's tail read.
      const current = controller.current?.signal;
      if (again.current && current && !current.aborted) { again.current = false; void sync(current); }
    }
  }
  const syncNow = useRef(sync);
  syncNow.current = sync;

  useEffect(() => {
    if (!mount.current) return;
    const terminal = new Terminal({ disableStdin: true, cursorBlink: false, cursorStyle: "underline", cursorInactiveStyle: "none", convertEol: !attachable,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace", fontSize: 14, scrollback: 10000,
      theme: terminalThemeFor(theme) });
    const fit = new FitAddon();
    term.current = terminal;
    controller.current = new AbortController();
    // A fresh xterm (including React strict mode's second mount) always starts with a tail read.
    cursor.current = null;
    terminal.loadAddon(fit);
    terminal.open(mount.current);
    let resizeFrame = 0;
    const resize = () => {
      if (resizeFrame) cancelAnimationFrame(resizeFrame);
      resizeFrame = requestAnimationFrame(() => {
        resizeFrame = 0;
        if (mount.current?.clientWidth && mount.current.clientHeight) fit.fit();
      });
    };
    fitNow.current = resize;
    const observer = new ResizeObserver(resize);
    observer.observe(mount.current);
    if (mount.current.parentElement) observer.observe(mount.current.parentElement);
    window.addEventListener("resize", resize);
    window.visualViewport?.addEventListener("resize", resize);
    return () => {
      observer.disconnect(); window.removeEventListener("resize", resize);
      window.visualViewport?.removeEventListener("resize", resize);
      if (resizeFrame) cancelAnimationFrame(resizeFrame);
      controller.current?.abort(); controller.current = null;
      terminal.dispose(); term.current = null;
      fitNow.current = () => {};
    };
  }, []);

  useEffect(() => {
    const applyTheme = () => { if (term.current) term.current.options.theme = terminalThemeFor(theme); };
    applyTheme();
    if (theme !== "system") return;
    const colorScheme = window.matchMedia("(prefers-color-scheme: dark)");
    colorScheme.addEventListener("change", applyTheme);
    return () => colorScheme.removeEventListener("change", applyTheme);
  }, [theme]);

  useEffect(() => {
    const frame = requestAnimationFrame(() => fitNow.current());
    return () => cancelAnimationFrame(frame);
  }, [expanded]);

  // A dead cell is not polled: the read on its state change picks up the final bytes.
  useEffect(() => {
    const signal = controller.current?.signal;
    if (signal) void syncNow.current(signal);
  }, [revision, cellState]);

  useEffect(() => {
    if (!live) return;
    const timer = setInterval(() => {
      const signal = controller.current?.signal;
      if (signal && document.visibilityState === "visible") void syncNow.current(signal);
    }, FOLLOW_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [live]);

  return <div className="terminal-session terminal-log">
    {empty && !error && <p className="empty-state terminal-empty" role="status">No output yet.</p>}
    {error && <ErrorNotice error={error} kind="Container log" />}
    {omitted && !empty && <p className="terminal-log-omitted">Earlier output is not shown.</p>}
    <div className="terminal-screen" hidden={empty} aria-label={`Log for ${scope.join(" / ")} / ${container}`}>
      {/* FitAddon sizes from the xterm parent's computed box; keep padding off it so rows/cols never spill under the panel border. */}
      <div ref={mount} className="terminal-mount" />
    </div>
  </div>;
}
