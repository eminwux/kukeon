import { useEffect, useState } from "react";
import { apiPath, kinds, titles } from "./routes";
import type { Failure, Resource } from "./request";

export type SetupStatus = { name?: string; index?: number; state: string; error?: string };
export type RuntimeStatus = {
  state?: string; name?: string; id?: string; cgroupReady?: boolean;
  reason?: string; message?: string; outOfSync?: boolean; outOfSyncReason?: string; outOfSyncError?: string;
  restartCount?: number; exitCode?: number; exitSignal?: string;
  createdAt?: string; updatedAt?: string; startTime?: string; finishTime?: string; restartTime?: string;
  containers?: RuntimeStatus[]; repos?: SetupStatus[]; stages?: SetupStatus[];
};
export type Doc = { metadata: { name: string; labels?: Record<string, string> }; status?: RuntimeStatus; apiVersion?: string; kind?: string; [key: string]: unknown };
export type Row = { doc: Doc; scope: string[] };
export type Inventory = { rows: Row[][]; failures: string[]; updatedAt: string };
export type InventoryResource = Resource<Inventory> & { daemon: Resource<true> };

async function readJSON<T>(path: string, signal: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal });
  const body = await response.json();
  if (!response.ok) throw body.error ?? { code: "unavailable", message: `HTTP ${response.status}` };
  return body;
}

// Limit fan-out across scopes so large inventories do not overwhelm the daemon.
async function eachScope(rows: Row[], read: (row: Row) => Promise<void>) {
  let cursor = 0;
  await Promise.all(Array.from({ length: Math.min(6, rows.length) }, async () => {
    while (cursor < rows.length) await read(rows[cursor++]);
  }));
}

export function useInventory(revision: number): InventoryResource {
  const [state, setState] = useState<Resource<Inventory>>({ loading: true });
  const [daemon, setDaemon] = useState<Resource<true>>({ loading: true });
  const [pulse, setPulse] = useState(0);
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") setPulse(value => value + 1);
    }, 60_000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setState(old => ({ data: old.data, loading: true, error: old.error }));
    setDaemon(old => ({ ...old, loading: true }));
    void (async () => {
      try {
        const realms = await readJSON<Doc[]>("/api/realms", controller.signal);
        if (controller.signal.aborted) return;
        setDaemon({ data: true, loading: false });
        const result: Inventory = { rows: [realms.map(doc => ({ doc, scope: [doc.metadata.name] })), [], [], []], failures: [], updatedAt: "" };
        for (let depth = 1; depth < 4; depth++) {
          await eachScope(result.rows[depth - 1], async parent => {
            try {
              const docs = await readJSON<Doc[]>(`${apiPath(parent.scope)}/${kinds[depth]}s`, controller.signal);
              result.rows[depth].push(...docs.map(doc => ({ doc, scope: [...parent.scope, doc.metadata.name] })));
            } catch (error) {
              if (controller.signal.aborted) throw error;
              result.failures.push(`${titles[depth]} in ${parent.scope.join(" / ")}: ${(error as Failure).message ?? "Request failed"}`);
            }
          });
          result.rows[depth].sort((a, b) => a.scope.join("/").localeCompare(b.scope.join("/")));
        }
        if (controller.signal.aborted) return;
        result.updatedAt = new Date().toISOString();
        setState(old => result.failures.length && old.data
          ? { data: old.data, loading: false, error: { code: "partial", message: result.failures.join("; ") } }
          : { data: result, loading: false });
      } catch (error) {
        if (controller.signal.aborted) return;
        const failure = error as Failure;
        const detail = { code: failure.code ?? "unavailable", message: failure.message ?? "Request failed" };
        setDaemon({ loading: false, error: detail });
        setState(old => ({ data: old.data, loading: false, error: detail }));
      }
    })();
    return () => controller.abort();
  }, [revision, pulse]);
  return { ...state, daemon };
}
