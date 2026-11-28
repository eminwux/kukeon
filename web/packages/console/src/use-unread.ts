import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { applyReadings, leftCells, markersKey, parseActivity, parseMarkers, pruneMarkers, readingCells, unreadState, type Markers, type Readings, type Unread } from "./unread.mjs";
import { apiFetch } from "@kukeon/api-client";

export type { Unread } from "./unread.mjs";

// The route takes at most 100 cell parameters; more cells are asked for in further requests, one at a time.
const maxCellsPerRequest = 100;

// The read markers (#1696), shared by every component and kept in step with the other tabs of this
// browser through the storage event. A missing or unreadable key reads as empty; a failed write keeps
// the value for this page only, like settings.ts.
function readStored(): Markers {
  try { return parseMarkers(localStorage.getItem(markersKey)); } catch { return {}; }
}
let markers: Markers = readStored();
const listeners = new Set<() => void>();
function notify() { for (const listener of listeners) listener(); }
function writeMarkers(next: Markers) {
  markers = next;
  try { localStorage.setItem(markersKey, JSON.stringify(next)); } catch { /* Read state is optional. */ }
  notify();
}
function subscribe(listener: () => void) {
  if (!listeners.size) window.addEventListener("storage", onStorage);
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (!listeners.size) window.removeEventListener("storage", onStorage);
  };
}
function onStorage(event: StorageEvent) {
  if (event.key !== null && event.key !== markersKey) return;
  markers = readStored();
  notify();
}

function activityURL(scopes: string[][]): string {
  return "/terminal-activity?" + scopes.map(scope => `cell=${scope.map(encodeURIComponent).join("/")}`).join("&");
}

// readActivity asks for scopes in requests of at most 100 cells, one after the other. A failed request
// leaves its cells out, so they keep their last reading.
async function readActivity(scopes: string[][], signal: AbortSignal): Promise<Readings> {
  let readings: Readings = {};
  for (let i = 0; i < scopes.length && !signal.aborted; i += maxCellsPerRequest) {
    try {
      const response = await apiFetch(activityURL(scopes.slice(i, i + maxCellsPerRequest)), { headers: { Accept: "application/json" }, signal });
      if (response.ok) readings = { ...readings, ...parseActivity(await response.json()) };
    } catch { /* Kept as the last reading. */ }
  }
  return readings;
}

// useUnread polls GET /api/terminal-activity for scopes (the cells the Workbench sidebar lists) while
// enabled and the browser tab is visible: an immediate pass, then one every pollMs (the Unread check
// interval setting, #1770). A new pollMs restarts the timer. A tick is skipped while a request is in
// flight, and everything aborts on unmount, like useCellScreens.
//
// quietMs is how long a terminal must be quiet before its new output marks the cell (Mark unread
// after quiet for); 0 marks it on the first new byte.
//
// reading holds the cell keys whose terminals the operator is looking at, one per view (#1777): the main
// cell screen's and the open side panel's active tab, each while its session is attached and the tab is
// visible; an empty entry is a view that reads nothing. Every reading of those cells moves their markers
// to the terminal's output count. When a cell stops being read in every view, one immediate request
// reads that cell and moves its marker, so output seen in the last seconds before leaving is not
// reported as unread; this is the one request made whatever enabled and the tab's visibility say.
//
// inventoryKeys are the keys of every cell of a complete inventory, or null while it is loading or
// incomplete: markers of the cells not in it are pruned.
export function useUnread(scopes: string[][], reading: (string | null)[], enabled: boolean, inventoryKeys: string[] | null, pollMs: number, quietMs: number): Record<string, Unread> {
  const stored = useSyncExternalStore(subscribe, () => markers);
  const [readings, setReadings] = useState<Readings>({});
  const [leaving, setLeaving] = useState<string[]>([]);
  const latest = useRef(scopes);
  latest.current = scopes;
  const readingKey = JSON.stringify(readingCells(reading));
  const read = useMemo(() => JSON.parse(readingKey) as string[], [readingKey]);
  const beingRead = useMemo(() => new Set([...read, ...leaving]), [read, leaving]);
  const beingReadRef = useRef(beingRead);
  beingReadRef.current = beingRead;
  // One request at a time: the polls and the leave readings share this chain.
  const queue = useRef(Promise.resolve());
  const pending = useRef(0);
  const lifetime = useRef<AbortController | null>(null);
  const pass = useRef<(() => void) | null>(null);
  const scopesKey = JSON.stringify(scopes);

  function enqueue(task: () => Promise<void>) {
    pending.current++;
    queue.current = queue.current.then(task).catch(() => { /* A failed read keeps the last one. */ }).finally(() => { pending.current--; });
  }
  function apply(next: Readings, alsoRead?: string) {
    if (!Object.keys(next).length) return;
    setReadings(old => ({ ...old, ...next }));
    const read = alsoRead ? new Set([...beingReadRef.current, alsoRead]) : beingReadRef.current;
    const result = applyReadings(markers, next, read);
    if (result.changed.length) writeMarkers(result.markers);
  }

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => { controller.abort(); lifetime.current = null; };
  }, []);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    const run = () => {
      if (pending.current || controller.signal.aborted || document.visibilityState !== "visible") return;
      const scopes = latest.current;
      if (!scopes.length) return;
      enqueue(async () => {
        const next = await readActivity(scopes, controller.signal);
        if (!controller.signal.aborted) apply(next);
      });
    };
    pass.current = run;
    run();
    const timer = window.setInterval(run, pollMs);
    const onVisibility = () => { if (document.visibilityState === "visible") run(); };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibility);
      controller.abort();
      pass.current = null;
    };
  }, [enabled, pollMs]);

  // A listed cell with no reading yet (the first inventory, a new cell) is read now, not on the next tick.
  useEffect(() => {
    if (latest.current.some(scope => !readings[JSON.stringify(scope)])) pass.current?.();
    // readings is read, not watched: only a change of the listed cells starts a pass.
  }, [scopesKey]);

  // Leaving a cell: read it once more and treat it as read until that reading lands.
  const previous = useRef(read);
  useEffect(() => {
    const before = previous.current;
    previous.current = read;
    for (const left of leftCells(before, read)) {
      let scope: string[];
      try { scope = JSON.parse(left); } catch { continue; }
      setLeaving(old => old.includes(left) ? old : [...old, left]);
      enqueue(async () => {
        const signal = lifetime.current?.signal;
        if (!signal || signal.aborted) return;
        try {
          const next = await readActivity([scope], signal);
          if (!signal.aborted) apply(next, left);
        } finally {
          if (!signal.aborted) setLeaving(old => old.filter(key => key !== left));
        }
      });
    }
  }, [read]);

  const inventoryId = inventoryKeys ? inventoryKeys.join("\n") : null;
  useEffect(() => {
    if (!inventoryKeys) return;
    const next = pruneMarkers(markers, inventoryKeys);
    if (Object.keys(next).length !== Object.keys(markers).length) writeMarkers(next);
  }, [inventoryId]);

  return useMemo(() => unreadState(stored, readings, beingRead, quietMs), [stored, readings, beingRead, quietMs]);
}

// useTabVisible follows document.visibilityState.
export function useTabVisible(): boolean {
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  useEffect(() => {
    const changed = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", changed);
    return () => document.removeEventListener("visibilitychange", changed);
  }, []);
  return visible;
}
