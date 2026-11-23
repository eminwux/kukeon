import { useEffect, useState } from "react";
import { apiPath } from "./routes";
import type { Resource } from "./request";
import type { SpaceNetwork } from "./network-format.js";

// At most this many Space network reads run at once, as the inventory's own fan-out.
const concurrency = 6;

// useSpaceSubnets reads the network detail of each given Space — the rows on the Spaces table's current
// page — for its subnet (#1630). A Space document has no CIDR; GET …/spaces/{space}/network reads it from
// network.json and the CNI conflist. Reads are aborted when the page, the filter or the refresh changes.
// Keyed by JSON.stringify(scope).
export function useSpaceSubnets(scopes: string[][], refresh: string | number): Record<string, Resource<SpaceNetwork>> {
  const key = JSON.stringify(scopes);
  const [state, setState] = useState<Record<string, Resource<SpaceNetwork>>>({});
  useEffect(() => {
    const list = JSON.parse(key) as string[][];
    if (!list.length) return;
    const controller = new AbortController();
    setState(old => Object.fromEntries(list.map(scope => { const id = JSON.stringify(scope); return [id, { data: old[id]?.data, loading: true }]; })));
    const settle = (id: string, value: Resource<SpaceNetwork>) => { if (!controller.signal.aborted) setState(old => ({ ...old, [id]: value })); };
    let cursor = 0;
    const read = async (scope: string[]) => {
      const id = JSON.stringify(scope);
      try {
        const response = await fetch(`${apiPath(scope)}/network`, { signal: controller.signal, headers: { Accept: "application/json" } });
        const body = await response.json();
        if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
        // Anything but a detail with a space section is not a subnet read.
        const space = body && !Array.isArray(body) && typeof body.space === "object" ? body.space as SpaceNetwork : undefined;
        if (!space) throw { code: "unavailable", message: "The response carries no space network." };
        settle(id, { data: space, loading: false });
      } catch (error) {
        if (controller.signal.aborted) return;
        const failure = error as { code?: string; message?: string };
        settle(id, { loading: false, error: { code: failure.code ?? "unavailable", message: failure.message ?? "Request failed" } });
      }
    };
    void Promise.all(Array.from({ length: Math.min(concurrency, list.length) }, async () => {
      while (cursor < list.length && !controller.signal.aborted) await read(list[cursor++]);
    }));
    return () => controller.abort();
  }, [key, refresh]);
  return state;
}
