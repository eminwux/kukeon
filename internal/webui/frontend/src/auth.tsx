import { useCallback, useEffect, useState, type FormEvent } from "react";
import { failureTitle, toFailure, type Failure } from "./request";
import { BrandMark } from "./sidebar-shell";

// The web UI login (#1652). `kuke serve` gates every /api/ route behind a
// session cookie set by POST /api/login; the password is the web-ui-admin
// secret `kuke init` creates.

export const loginUser = "root";
export const loginReadBack = "kuke get secret web-ui-admin --realm kuke-system -o yaml";
// Fired whenever an API request answers 401 during a session.
export const unauthenticatedEvent = "kukeon-unauthenticated";

function requestPath(input: RequestInfo | URL): string {
  const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  try { return new URL(raw, location.href).pathname; } catch { return ""; }
}

let guardInstalled = false;
// installFetchGuard wraps window.fetch once so every API request that answers
// 401 signs the UI out. A failed login is the expected answer to a wrong
// password, so POST /api/login is exempt.
export function installFetchGuard() {
  if (guardInstalled) return;
  guardInstalled = true;
  const original = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const response = await original(input, init);
    const path = requestPath(input);
    if (response.status === 401 && path.startsWith("/api/") && path !== "/api/login") {
      window.dispatchEvent(new Event(unauthenticatedEvent));
    }
    return response;
  };
}

// checkSessionAfterSocketFailure asks the server whether the session still
// holds after a WebSocket failed to open: the browser hides the upgrade's
// status code, so a 401 surfaces only through this request (and the guard).
export function checkSessionAfterSocketFailure() {
  void fetch("/api/session", { headers: { Accept: "application/json" } }).catch(() => { /* A network failure keeps the session. */ });
}

export type Session =
  | { phase: "checking" }
  | { phase: "signed-out" }
  | { phase: "unavailable"; error: Failure }
  | { phase: "signed-in"; user: string };

async function errorOf(response: Response): Promise<Failure> {
  try {
    const body = await response.json();
    if (body?.error && typeof body.error.code === "string") return { code: body.error.code, message: String(body.error.message ?? "") };
  } catch { /* Use the HTTP status below. */ }
  return { code: response.status === 401 ? "unauthenticated" : "unavailable", message: `HTTP ${response.status}` };
}

// useSession reads GET /api/session on mount, drops to signed-out on any 401
// during the session, and exposes sign-in and sign-out transitions.
export function useSession() {
  const [session, setSession] = useState<Session>({ phase: "checking" });
  const check = useCallback(async () => {
    setSession({ phase: "checking" });
    try {
      const response = await fetch("/api/session", { headers: { Accept: "application/json" } });
      if (response.ok) {
        const body = await response.json();
        setSession({ phase: "signed-in", user: typeof body?.user === "string" ? body.user : loginUser });
      } else if (response.status === 401) {
        setSession({ phase: "signed-out" });
      } else {
        setSession({ phase: "unavailable", error: await errorOf(response) });
      }
    } catch (error) {
      setSession({ phase: "unavailable", error: toFailure(error) });
    }
  }, []);
  useEffect(() => { installFetchGuard(); void check(); }, [check]);
  useEffect(() => {
    const signedOut = () => setSession(old => old.phase === "signed-in" ? { phase: "signed-out" } : old);
    window.addEventListener(unauthenticatedEvent, signedOut);
    return () => window.removeEventListener(unauthenticatedEvent, signedOut);
  }, []);
  const signedIn = useCallback((user: string) => setSession({ phase: "signed-in", user }), []);
  const signOut = useCallback(async () => {
    try {
      await fetch("/api/logout", { method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: "{}" });
    } catch { /* The login screen shows either way; the cookie expires on the next successful call. */ }
    setSession({ phase: "signed-out" });
  }, []);
  return { session, retry: check, signedIn, signOut };
}

// Login is the only screen without a valid session: a user/password form,
// the command that prints the password, and the daemon notice when the
// session check could not reach kukeond.
export function Login({ notice, checking, onRetry, onSignedIn }: {
  notice?: Failure;
  checking?: boolean;
  onRetry: () => void;
  onSignedIn: (user: string) => void;
}) {
  const [user, setUser] = useState(loginUser);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<Failure>();
  const [pending, setPending] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setError(undefined);
    try {
      const response = await fetch("/api/login", {
        method: "POST",
        headers: { Accept: "application/json", "Content-Type": "application/json" },
        body: JSON.stringify({ username: user, password }),
      });
      if (response.ok) {
        const body = await response.json().catch(() => ({}));
        onSignedIn(typeof body?.user === "string" ? body.user : user);
        return;
      }
      setError(await errorOf(response));
      setPassword("");
    } catch (failure) {
      setError(toFailure(failure));
    } finally {
      setPending(false);
    }
  }
  return <main className="login-screen" id="main-content">
    <form className="login-card" onSubmit={submit} aria-labelledby="login-title">
      <div className="login-brand"><BrandMark /><span className="brand-title">kukeon</span></div>
      <h1 id="login-title">Sign in</h1>
      {checking && <p className="login-status" role="status">Checking session…</p>}
      {notice && <div className="error" role="alert">
        <strong>{failureTitle(notice)}</strong><p>{notice.message}</p>
        <button type="button" onClick={onRetry}>Retry</button>
      </div>}
      <label>User<input name="username" autoComplete="username" value={user} onChange={event => setUser(event.target.value)} required /></label>
      <label>Password<input name="password" type="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.target.value)} required autoFocus /></label>
      {error && <div className="error" role="alert"><p>{error.message}</p></div>}
      <button className="primary-action" type="submit" disabled={pending}>{pending ? "Signing in…" : "Sign in"}</button>
      <p className="login-hint">Print the password on the host with <code>{loginReadBack}</code></p>
    </form>
  </main>;
}
