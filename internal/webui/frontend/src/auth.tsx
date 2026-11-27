import { useCallback, useEffect, useState, type ComponentType, type FormEvent } from "react";
import { failureTitle, toFailure, type Failure } from "./request";
import { BrandMark } from "./sidebar-shell";
import { apiFetch, onUnauthorized } from "./transport";

// The web UI login (#1652). `kuke serve` gates every /api/ route behind a
// session cookie set by POST /api/login; the password is the web-ui-admin
// secret `kuke init` creates.
//
// The auth seam (#1752): session state, sign-in, sign-out and 401 handling go
// through an AuthAdapter. The default, localAuth, is that local root sign-in.

export const loginUser = "root";
export const loginReadBack = "kuke get secret web-ui-admin --realm kuke-system -o yaml";

export type SessionCheck =
  | { phase: "signed-out" }
  | { phase: "unavailable"; error: Failure }
  | { phase: "signed-in"; user: string };

export type Session = { phase: "checking" } | SessionCheck;

// SignInProps are what the console hands the sign-in screen: the daemon notice
// when the session check could not reach kukeond, whether a check is running,
// a retry, and the transition to signed in.
export type SignInProps = {
  notice?: Failure;
  checking?: boolean;
  onRetry: () => void;
  onSignedIn: (user: string) => void;
};

export type AuthAdapter = {
  // Reads whether a session holds.
  session: () => Promise<SessionCheck>;
  // Ends the session; the sign-in screen shows afterwards either way.
  signOut: () => Promise<void>;
  // Whether a 401 answer to the API path ends the session.
  endsSession: (path: string) => boolean;
  // The screen shown without a session.
  SignIn: ComponentType<SignInProps>;
};

async function errorOf(response: Response): Promise<Failure> {
  try {
    const body = await response.json();
    if (body?.error && typeof body.error.code === "string") return { code: body.error.code, message: String(body.error.message ?? "") };
  } catch { /* Use the HTTP status below. */ }
  return { code: response.status === 401 ? "unauthenticated" : "unavailable", message: `HTTP ${response.status}` };
}

// localAuth is kuke serve's own sign-in: GET /api/session, POST /api/logout and
// the Login screen. A failed login is the expected answer to a wrong password,
// so a 401 from POST /api/login does not end the session.
export const localAuth: AuthAdapter = {
  async session() {
    try {
      const response = await apiFetch("/session", { headers: { Accept: "application/json" } });
      if (response.ok) {
        const body = await response.json();
        return { phase: "signed-in", user: typeof body?.user === "string" ? body.user : loginUser };
      }
      if (response.status === 401) return { phase: "signed-out" };
      return { phase: "unavailable", error: await errorOf(response) };
    } catch (error) {
      return { phase: "unavailable", error: toFailure(error) };
    }
  },
  async signOut() {
    try {
      await apiFetch("/logout", { method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: "{}" });
    } catch { /* The login screen shows either way; the cookie expires on the next successful call. */ }
  },
  endsSession: path => path !== "/login",
  SignIn: Login,
};

let current: AuthAdapter = localAuth;

// configureAuth replaces the auth adapter; mountConsole calls it before the first render.
export function configureAuth(adapter: AuthAdapter = localAuth) {
  current = adapter;
}

export function auth(): AuthAdapter {
  return current;
}

const endedListeners = new Set<() => void>();
function sessionEnded() {
  for (const listener of [...endedListeners]) listener();
}

// checkSessionAfterSocketFailure asks the adapter whether the session still
// holds after a WebSocket failed to open: the browser hides the upgrade's
// status code, so a 401 surfaces only through this request.
export function checkSessionAfterSocketFailure() {
  void current.session().then(result => { if (result.phase === "signed-out") sessionEnded(); }, () => { /* A failed check keeps the session. */ });
}

// useSession reads the session on mount, drops to signed-out on any 401 the
// adapter says ends it, and exposes sign-in and sign-out transitions.
export function useSession() {
  const [session, setSession] = useState<Session>({ phase: "checking" });
  const check = useCallback(async () => {
    setSession({ phase: "checking" });
    try {
      setSession(await current.session());
    } catch (error) {
      setSession({ phase: "unavailable", error: toFailure(error) });
    }
  }, []);
  useEffect(() => { void check(); }, [check]);
  useEffect(() => {
    const signedOut = () => setSession(old => old.phase === "signed-in" ? { phase: "signed-out" } : old);
    const stopUnauthorized = onUnauthorized(path => { if (current.endsSession(path)) signedOut(); });
    endedListeners.add(signedOut);
    return () => { stopUnauthorized(); endedListeners.delete(signedOut); };
  }, []);
  const signedIn = useCallback((user: string) => setSession({ phase: "signed-in", user }), []);
  const signOut = useCallback(async () => {
    try { await current.signOut(); } catch { /* The sign-in screen shows either way. */ }
    setSession({ phase: "signed-out" });
  }, []);
  return { session, retry: check, signedIn, signOut };
}

// Login is the only screen without a valid session: a user/password form,
// the command that prints the password, and the daemon notice when the
// session check could not reach kukeond.
export function Login({ notice, checking, onRetry, onSignedIn }: SignInProps) {
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
      const response = await apiFetch("/login", {
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
