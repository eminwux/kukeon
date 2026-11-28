// The transport seam (#1752): the one module that knows where the API lives. Every API request and
// every attach/exec WebSocket goes through it, so the console can be mounted under another base
// path or carried over another transport without touching the pages.
//
// Paths handed to apiFetch and apiSocket are relative to the API root: "/realms", not "/api/realms".

export type Transport = {
  // The API root every request path is appended to; "/api" by default.
  apiBase: string;
  // Performs one HTTP request to a full URL (apiBase + path).
  fetch: (input: string, init?: RequestInit) => Promise<Response>;
  // The WebSocket URL of an API path.
  wsUrl: (path: string) => string;
};

export const defaultApiBase = "/api";

// sameOriginSocket is the WebSocket URL of base + path on the page's own host: wss: on an https:
// page, ws: otherwise.
function sameOriginSocket(base: string, path: string): string {
  const url = new URL(base + path, location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.href;
}

// trimTrailingSlashes drops every trailing "/" of base. A loop rather than /\/+$/, whose backtracking
// is quadratic on a long run of slashes followed by another character (CodeQL js/polynomial-redos).
function trimTrailingSlashes(base: string): string {
  let end = base.length;
  while (end > 0 && base[end - 1] === "/") end--;
  return base.slice(0, end);
}

// createTransport fills in what an override leaves out: the page's fetch (looked up per call, so a
// test or polyfill that replaces window.fetch later still applies) and a same-origin WebSocket URL
// under the transport's own apiBase.
export function createTransport(options: Partial<Transport> = {}): Transport {
  const apiBase = trimTrailingSlashes(options.apiBase ?? defaultApiBase);
  return {
    apiBase,
    fetch: options.fetch ?? ((input, init) => window.fetch(input, init)),
    wsUrl: options.wsUrl ?? (path => sameOriginSocket(apiBase, path)),
  };
}

let current = createTransport();

// configureTransport replaces the transport; mountConsole calls it before the first render.
export function configureTransport(options: Partial<Transport> = {}): Transport {
  current = createTransport(options);
  return current;
}

export function transport(): Transport {
  return current;
}

// apiUrl is the full URL of an API path under the current transport.
export function apiUrl(path: string): string {
  return current.apiBase + path;
}

type UnauthorizedListener = (path: string) => void;
const unauthorizedListeners = new Set<UnauthorizedListener>();

// onUnauthorized calls listener with the API path of every response that answers 401; the auth
// adapter decides whether that ends the session. It returns the unsubscribe.
export function onUnauthorized(listener: UnauthorizedListener): () => void {
  unauthorizedListeners.add(listener);
  return () => { unauthorizedListeners.delete(listener); };
}

// apiFetch performs one API request.
export async function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  const response = await current.fetch(apiUrl(path), init);
  if (response.status === 401) for (const listener of [...unauthorizedListeners]) listener(path);
  return response;
}

// apiSocket opens a WebSocket to an API path.
export function apiSocket(path: string): WebSocket {
  return new WebSocket(current.wsUrl(path));
}
