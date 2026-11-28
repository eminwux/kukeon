import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Icon, rememberFocus } from "@kukeon/ui";
import { terminalThemeFor, type Theme } from "./terminal";
import { RecordingSource, type RecordingFormat, type SourceError } from "./recording-source";
import { addLines, createRecording, duration, exportCast, opsDue, positionOf, runMarks, sizeAt, wallClockAt, writesBetween, type Recording } from "./recording.mjs";

// Replay (issue #1783): a read-only player for a container's whole terminal recording, from the oldest
// output sbsh retains. It opens no terminal session, so it uses none of the attach connections or
// retained surfaces, and nothing typed, pasted or resized in it reaches the container. Shaped like
// the Exec panel: it takes the place of the terminal or log while it is open.

const SPEEDS = [1, 2, 4, 8] as const;
const SEEK_STEP = 5;
// One write's bound while seeking, so a long recording is written in pieces and the controls stay usable.
const SEEK_WRITE_CHARS = 256 * 1024;
// Writes allowed in flight during playback before the player waits for xterm to catch up.
const MAX_PENDING_WRITES = 2;
const fontFamily = "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace";

export function ReplayButton({ container, open, onToggle }: { container: string; open: boolean; onToggle: () => void }) {
  const tooltip = open ? `Close Replay for ${container}` : `Replay the recorded terminal of ${container}`;
  return <button type="button" className="icon-button replay-button" aria-label="Replay" aria-expanded={open} title={tooltip} data-tooltip={tooltip} onClick={onToggle}><Icon name="replay" /></button>;
}

const clock = (seconds: number) => {
  const whole = Math.max(0, Math.floor(seconds));
  const h = Math.floor(whole / 3600), m = Math.floor(whole / 60) % 60, s = whole % 60;
  return `${h ? `${h}:${String(m).padStart(2, "0")}` : m}:${String(s).padStart(2, "0")}`;
};
const wallClock = (unix: number | null) => unix ? new Date(unix * 1000).toLocaleString() : "";

type Status = { phase: "loading" | "ready"; read: number; total: number } | { phase: "error"; error: SourceError };

// live: the cell is running, so its recording grows and Live returns to the attached terminal.
export function ReplayPanel({ scope, container, live, theme, expanded, onClose }: { scope: string[]; container: string; live: boolean; theme: Theme; expanded: boolean; onClose: () => void }) {
  const id = useId();
  const panel = useRef<HTMLElement>(null);
  const screen = useRef<HTMLDivElement>(null);
  const mount = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const recording = useRef<Recording>(createRecording());
  const [format, setFormat] = useState<RecordingFormat | null>(null);
  const [dropped, setDropped] = useState(false);
  const [status, setStatus] = useState<Status>({ phase: "loading", read: 0, total: 0 });
  // Bumped when the recording grows, so the timeline re-renders.
  const [, setGrowth] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [position, setPosition] = useState(0);
  const [speed, setSpeed] = useState<(typeof SPEEDS)[number]>(1);
  const [idleSkip, setIdleSkip] = useState(true);
  const [seeking, setSeeking] = useState(false);
  // The player's working state, read by the animation loop without re-rendering.
  const player = useRef({ written: 0, position: 0, playing: false, speed: 1, idleSkip: true, base: 0, baseAt: 0, pending: 0, seekGeneration: 0, seeking: false, following: false });
  const liveRef = useRef(live);
  liveRef.current = live;

  // Scale the terminal, at its recorded size, to fit the panel instead of refitting its columns.
  const scale = useCallback(() => {
    const element = term.current?.element;
    if (!element || !screen.current || !mount.current) return;
    if (fit.current) { if (mount.current.clientWidth && mount.current.clientHeight) fit.current.fit(); return; }
    mount.current.style.transform = "";
    const width = element.offsetWidth, height = element.offsetHeight;
    const room = screen.current.getBoundingClientRect();
    if (!width || !height || !room.width || !room.height) return;
    const factor = Math.min(1, (room.width - 20) / width, (room.height - 20) / height);
    mount.current.style.transform = `scale(${factor})`;
  }, []);

  const write = useCallback((data: string) => {
    const p = player.current;
    p.pending++;
    term.current?.write(data, () => { p.pending--; });
  }, []);
  // Queued behind the writes before it, so a size change never lands in the middle of earlier output.
  const resize = useCallback((cols: number, rows: number) => {
    term.current?.write("", () => {
      if (!term.current || (term.current.cols === cols && term.current.rows === rows)) return;
      term.current.resize(cols, rows);
      requestAnimationFrame(scale);
    });
  }, [scale]);
  const writeOps = useCallback((from: number, to: number, maxChars?: number) => {
    for (const step of writesBetween(recording.current, from, to, maxChars)) {
      if (step.data !== undefined) write(step.data); else resize(step.cols, step.rows);
    }
  }, [write, resize]);
  const drained = () => new Promise<void>(resolve => { if (term.current) term.current.write("", resolve); else resolve(); });

  // Seek: forward writes the ops in between; backward resets and writes everything up to the target.
  // Either way the screen equals the screen at that time when played from the start. A newer seek
  // supersedes this one between pieces.
  const seek = useCallback(async (target: number) => {
    const p = player.current;
    const rec = recording.current;
    const end = duration(rec, p.idleSkip);
    target = Math.min(Math.max(0, target), end);
    const generation = ++p.seekGeneration;
    p.seeking = true; setSeeking(true);
    p.position = target; p.base = target; p.baseAt = performance.now();
    p.following = false;
    setPosition(target);
    await drained();
    if (generation !== p.seekGeneration || !term.current) return;
    const due = opsDue(rec, p.idleSkip, target);
    if (due < p.written) {
      term.current.reset();
      const size = sizeAt(rec, 0);
      if (term.current.cols !== size.cols || term.current.rows !== size.rows) { term.current.resize(size.cols, size.rows); scale(); }
      p.written = 0;
    }
    while (p.written < due) {
      const next = Math.min(due, p.written + 2000);
      writeOps(p.written, next, SEEK_WRITE_CHARS);
      p.written = next;
      await drained();
      if (generation !== p.seekGeneration) return;
    }
    p.base = p.position; p.baseAt = performance.now();
    p.seeking = false; setSeeking(false);
  }, [scale, writeOps]);
  const seekRef = useRef(seek);
  seekRef.current = seek;

  // The terminal, created once at the recording's first size; disableStdin keeps it read-only.
  useEffect(() => {
    if (!mount.current) return;
    const terminal = new Terminal({ disableStdin: true, cursorBlink: false, cursorStyle: "underline", cursorInactiveStyle: "none", fontFamily, fontSize: 14, scrollback: 1000, cols: 80, rows: 24, theme: terminalThemeFor(theme) });
    term.current = terminal;
    terminal.open(mount.current);
    const observer = new ResizeObserver(() => requestAnimationFrame(scale));
    if (screen.current) observer.observe(screen.current);
    return () => { observer.disconnect(); terminal.dispose(); term.current = null; fit.current = null; };
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
    const frame = requestAnimationFrame(scale);
    return () => cancelAnimationFrame(frame);
  }, [expanded, scale]);

  // Load the recording; a raw one is written as it arrives, into a scrolling terminal fitted to the panel.
  useEffect(() => {
    let first = true;
    let growthFrame = 0;
    const grew = () => { if (!growthFrame) growthFrame = requestAnimationFrame(() => { growthFrame = 0; setGrowth(n => n + 1); }); };
    const source = new RecordingSource(scope, container, {
      begin(kind, wasDropped) {
        setFormat(kind);
        setDropped(wasDropped);
        if (kind === "raw" && term.current && mount.current) {
          term.current.options.scrollback = 10000;
          const addon = new FitAddon();
          fit.current = addon;
          term.current.loadAddon(addon);
          requestAnimationFrame(scale);
        }
      },
      lines(lines) {
        const rec = recording.current;
        addLines(rec, lines);
        if (first && rec.ops.length) {
          first = false;
          const size = sizeAt(rec, 0);
          term.current?.resize(size.cols, size.rows);
          requestAnimationFrame(scale);
        }
        // At the end of a live recording, new output plays straight away.
        const p = player.current;
        if (p.following && !p.seeking) {
          const due = rec.ops.length;
          writeOps(p.written, due);
          p.written = due;
          p.position = duration(rec, p.idleSkip);
          p.base = p.position; p.baseAt = performance.now();
          setPosition(p.position);
        }
        grew();
      },
      bytes(bytes) { term.current?.write(bytes); },
      progress(read, total, loaded) { setStatus({ phase: loaded ? "ready" : "loading", read, total }); },
      error(error) { setStatus({ phase: "error", error }); },
    }, () => liveRef.current);
    source.start();
    return () => { source.close(); if (growthFrame) cancelAnimationFrame(growthFrame); };
  }, [scope.join("/"), container]);

  // Playback writes the ops that are due on each animation frame.
  useEffect(() => {
    if (!playing) return;
    const p = player.current;
    p.playing = true;
    p.base = p.position; p.baseAt = performance.now();
    let frame = 0;
    let shown = 0;
    const tick = (now: number) => {
      frame = requestAnimationFrame(tick);
      if (p.seeking) return;
      const rec = recording.current;
      const end = duration(rec, p.idleSkip);
      let target = p.base + (now - p.baseAt) / 1000 * p.speed;
      if (target >= end) {
        target = end;
        // A live recording is followed at its end; a finished one stops there, once its last ops are written.
        if (liveRef.current) p.following = true;
        else {
          writeOps(p.written, rec.ops.length);
          p.written = rec.ops.length;
          p.position = end; setPosition(end); setPlaying(false);
          return;
        }
      }
      if (p.pending < MAX_PENDING_WRITES) {
        const due = opsDue(rec, p.idleSkip, target);
        if (due > p.written) { writeOps(p.written, due); p.written = due; }
        p.position = target;
      }
      if (now - shown > 100) { shown = now; setPosition(p.position); }
    };
    frame = requestAnimationFrame(tick);
    return () => { cancelAnimationFrame(frame); p.playing = false; p.following = false; setPosition(p.position); };
  }, [playing, writeOps]);

  useEffect(() => {
    const p = player.current;
    p.base = p.position; p.baseAt = performance.now();
    p.speed = speed;
  }, [speed]);

  // Switching idle skipping keeps the screen: the position moves to the same op on the other clock.
  useEffect(() => {
    const p = player.current;
    if (p.idleSkip === idleSkip) return;
    p.idleSkip = idleSkip;
    p.position = positionOf(recording.current, idleSkip, p.written);
    p.base = p.position; p.baseAt = performance.now();
    setPosition(p.position);
  }, [idleSkip]);

  // Focus enters the panel when it opens and returns to the Replay button when it closes.
  useEffect(() => {
    const restore = rememberFocus();
    panel.current?.querySelector<HTMLButtonElement>(".replay-play")?.focus();
    return restore;
  }, []);

  function download() {
    const blob = new Blob([exportCast(recording.current)], { type: "application/x-asciicast" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `${[...scope, container].join("_")}.cast`;
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  const rec = recording.current;
  const total = duration(rec, idleSkip);
  const togglePlay = () => {
    if (playing) { setPlaying(false); return; }
    // Play at the end of a finished recording starts it again.
    if (!live && position >= total && total > 0) void seek(0);
    setPlaying(true);
  };
  function keys(event: KeyboardEvent<HTMLElement>) {
    const target = event.target as HTMLElement;
    if (event.key === "Escape" && !target.closest("select")) { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (format !== "asciicast" || target.closest("select, input[type=checkbox]")) return;
    if (event.key === " " && !target.closest("button")) { event.preventDefault(); togglePlay(); }
    else if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
      event.preventDefault();
      void seek(player.current.position + (event.key === "ArrowRight" ? SEEK_STEP : -SEEK_STEP));
    }
  }

  const failed = status.phase === "error" ? status.error : null;
  const outdated = failed?.code === "daemon_outdated";
  const loading = status.phase === "loading";
  const timed = format === "asciicast";
  const marks = timed ? runMarks(rec, idleSkip) : [];
  const empty = status.phase === "ready" && (timed ? !rec.ops.some(op => op.kind === "o") : false);
  const now = timed ? wallClockAt(rec, idleSkip, position) : null;
  const statusText = outdated ? "" : failed ? failed.message
    : loading ? `Loading the recording…${status.total ? ` (${status.read} of ${status.total} segments)` : ""}`
      : seeking ? "Seeking…" : "";

  return <section className={`replay-panel${expanded ? " is-expanded" : ""}`} ref={panel} role="group" aria-labelledby={`${id}-title`} onKeyDown={keys}>
    <header className="replay-panel-header">
      <h2 id={`${id}-title`}>Replay of <code>{container}</code></h2>
      <div className="replay-actions">
        {timed && <button type="button" onClick={download} disabled={!rec.ops.length}>Download</button>}
        {live && <button type="button" onClick={onClose}>Live</button>}
        <button type="button" className="replay-close" aria-label="Close Replay" onClick={onClose}>Close</button>
      </div>
    </header>
    {outdated && <p className="replay-notice" role="alert">kukeond must be upgraded to replay this terminal. The terminal and its log keep working.</p>}
    {dropped && <p className="replay-notice">Earlier output was dropped by retention: the recording starts at its oldest retained output.</p>}
    {format === "raw" && <p className="replay-notice">This recording has no timing: the container started before recordings were timed. Its output is shown from the oldest retained byte.</p>}
    {empty && <p className="replay-notice" role="status">No output recorded yet.</p>}
    {timed && <div className="replay-controls">
      <button type="button" className="icon-button replay-play" aria-label={playing ? "Pause" : "Play"} data-tooltip={playing ? "Pause (space)" : "Play (space)"} onClick={togglePlay} disabled={!rec.ops.length}><Icon name={playing ? "pause" : "start"} /></button>
      <div className="replay-timeline">
        <input type="range" aria-label="Position" min={0} max={total || 0} step="any" value={Math.min(position, total)} disabled={!rec.ops.length}
          aria-valuetext={`${clock(position)} of ${clock(total)}${now ? `, ${wallClock(now)}` : ""}`}
          onChange={event => void seek(Number(event.target.value))} />
        <div className="replay-marks" aria-hidden="true">
          {marks.map(mark => <span key={mark.index} className="replay-mark" style={{ left: `${total ? mark.position / total * 100 : 0}%` }} title={`Run ${mark.index + 1}: ${wallClock(mark.timestamp)}`} />)}
        </div>
      </div>
      <span className="replay-time" aria-live="off"><span className="replay-elapsed">{clock(position)} / {clock(total)}</span>{now ? <time className="replay-wall" dateTime={new Date(now * 1000).toISOString()}>{wallClock(now)}</time> : null}</span>
      {marks.length > 1 && <span className="replay-runs">{marks.length} runs</span>}
      <label className="replay-speed">Speed <select value={speed} onChange={event => setSpeed(Number(event.target.value) as (typeof SPEEDS)[number])}>{SPEEDS.map(value => <option key={value} value={value}>{value}×</option>)}</select></label>
      <label className="replay-idle"><input type="checkbox" checked={idleSkip} onChange={event => setIdleSkip(event.target.checked)} />Skip idle</label>
    </div>}
    {statusText && <p className="replay-status" role="status">{statusText}</p>}
    <div className="replay-screen" ref={screen} hidden={outdated} aria-label={`Replay for ${[...scope, container].join(" / ")}`}>
      <div ref={mount} className={`replay-mount${format === "raw" ? " is-fitted" : ""}`} />
    </div>
  </section>;
}
