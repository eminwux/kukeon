import { useEffect, useState } from "react";
import { apiFetch } from "./transport";

export type Failure = { code: string; message: string };
export type Resource<T> = { data?: T; loading: boolean; error?: Failure };

// Abort replaced requests and ignore their results, including on realm switches.
export function useResource<T>(path: string | null, revision: number): Resource<T> {
  const [state, setState] = useState<Resource<T>>({ loading: true });
  const [previousPath, setPreviousPath] = useState(path);
  // Clear the previous realm before painting a different selection.
  if (previousPath !== path) {
    setPreviousPath(path);
    setState({ loading: path !== null });
  }
  useEffect(() => {
    if (path === null) return;
    const controller = new AbortController();
    setState((old) => ({ data: old.data, loading: true }));
    void (async () => {
      try {
        const response = await apiFetch(path, { signal: controller.signal });
        const body = await response.json();
        if (!response.ok) throw body.error ?? { code: "http_error", message: `HTTP ${response.status}` };
        if (!controller.signal.aborted) setState({ data: body, loading: false });
      } catch (error) {
        if (controller.signal.aborted) return;
        const failure = error as Partial<Failure>;
        setState((old) => ({ data: old.data, loading: false,
          error: { code: failure.code ?? "unavailable", message: failure.message ?? "Request failed. Try refreshing." },
        }));
      }
    })();
    return () => controller.abort();
  }, [path, revision]);
  return state;
}

// Normalize a thrown API error body or a network exception into a Failure.
export function toFailure(error: unknown): Failure {
  const failure = error as Partial<Failure>;
  if (failure && typeof failure.code === "string") return { code: failure.code, message: failure.message ?? "Request failed." };
  return { code: "unavailable", message: error instanceof Error ? error.message : "The request could not reach the web server." };
}

export function failureTitle(error: Failure, kind = "Realm"): string {
  return error.code === "permission_denied" ? "Permission denied"
    : error.code === "unavailable" ? "Daemon unavailable"
    : error.code === "daemon_outdated" ? "Daemon outdated"
    : error.code === "log_unavailable" ? "Daemon logs unavailable"
    : error.code === "not_found" ? `${kind} no longer exists` : "Request failed";
}

export function ErrorNotice({ error, kind = "Realm" }: { error: Failure; kind?: string }) {
  return <div className="error" role="alert"><strong>{failureTitle(error, kind)}</strong><p>{error.message}</p></div>;
}
