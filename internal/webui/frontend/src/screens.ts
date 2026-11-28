import { useEffect, useRef, useState } from "react";
import { apiPath } from "./routes";
import { apiFetch } from "./transport";

// One tile's terminal screen (#1635): the last text read, whether the cell has no screen to read, and
// whether the last refresh failed so the text shown is old.
export type Screen = { text?: string; unavailable?: boolean; stale?: boolean };

const maxInFlight = 4;
const key = (scope: string[]) => JSON.stringify(scope);

// useCellScreens polls GET …/screenshot for the cells whose tiles are on screen (scopes): an immediate
// pass, then one every refreshMs (the Thumbnail refresh interval setting, #1770) while enabled and the
// browser tab is visible. A new refreshMs restarts the timer. A tick is skipped while a pass is still
// running, at most four requests are in flight, and everything aborts on unmount or when enabled turns
// false. A screenshot never attaches, so it does not count against terminal
// connections.
export function useCellScreens(scopes: string[][], enabled: boolean, refreshMs: number): Record<string, Screen> {
  const [screens, setScreens] = useState<Record<string, Screen>>({});
  const latest = useRef(scopes);
  latest.current = scopes;
  const pass = useRef<(() => void) | null>(null);
  const scopesKey = JSON.stringify(scopes);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    let running = false;
    const read = async (scope: string[]) => {
      let next: Screen | null = null;
      try {
        const response = await apiFetch(`${apiPath(scope)}/screenshot`, { headers: { Accept: "application/json" }, signal: controller.signal });
        let body: { text?: unknown; error?: { code?: string } } = {};
        try { body = await response.json(); } catch { /* Treated as a failed read below. */ }
        if (response.ok && typeof body.text === "string") next = { text: body.text };
        else if (response.status === 404 && body.error?.code === "screen_unavailable") next = { unavailable: true };
      } catch {
        if (controller.signal.aborted) return;
      }
      if (controller.signal.aborted) return;
      const id = key(scope);
      // Any other answer keeps the last screen and marks it stale.
      setScreens(old => ({ ...old, [id]: next ?? { text: old[id]?.text, stale: true } }));
    };
    const run = () => {
      if (running || controller.signal.aborted || document.visibilityState !== "visible") return;
      const queue = [...latest.current];
      if (!queue.length) return;
      running = true;
      let cursor = 0;
      void Promise.all(Array.from({ length: Math.min(maxInFlight, queue.length) }, async () => {
        while (cursor < queue.length && !controller.signal.aborted) await read(queue[cursor++]);
      })).finally(() => { running = false; });
    };
    pass.current = run;
    run();
    const timer = window.setInterval(run, refreshMs);
    return () => {
      window.clearInterval(timer);
      controller.abort();
      pass.current = null;
    };
  }, [enabled, refreshMs]);

  // A tile that scrolls onto the screen without a screen yet is read now, not on the next tick.
  useEffect(() => {
    if (latest.current.some(scope => !screens[key(scope)])) pass.current?.();
    // screens is read, not watched: only a change of the tiles on screen starts a pass.
  }, [scopesKey]);

  return screens;
}
