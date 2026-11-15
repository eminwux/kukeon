import { useRef, useState, type FormEvent } from "react";
import { ErrorNotice, type Failure } from "./request";

// RegistryCredential is the redacted realm entry kuke serve returns (#1407):
// the password is always blank, so it is never read from the document.
export type RegistryCredential = { serverAddress?: string; username: string };
type Notice = { kind: "success"; message: string } | { kind: "error"; error: Failure };

function serverLabel(server?: string): string {
  return server ? server : "Default (registry from image reference)";
}

// RegistryCredentialsPanel adds or replaces a realm's pull credential by
// server, matching `kuke create registry-credential`. The token is
// write-only: it is held only while the form is filled, sent in the POST
// body, and cleared after every submission.
export function RegistryCredentialsPanel({ realm, credentials, onSaved }: { realm: string; credentials: RegistryCredential[]; onSaved: () => void }) {
  const [server, setServer] = useState("");
  const [username, setUsername] = useState("");
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [notice, setNotice] = useState<Notice>();
  const usernameInput = useRef<HTMLInputElement>(null);
  const replacing = credentials.some(cred => (cred.serverAddress ?? "") === server.trim());

  function edit(cred: RegistryCredential) {
    setServer(cred.serverAddress ?? "");
    setUsername(cred.username);
    setToken("");
    setNotice(undefined);
    usernameInput.current?.focus();
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const target = server.trim();
    if (!username.trim() || !token) {
      setNotice({ kind: "error", error: { code: "invalid_request", message: !username.trim() ? "Username is required." : "Token is required." } });
      return;
    }
    setPending(true);
    setNotice(undefined);
    const body = JSON.stringify({ server: target, username: username.trim(), password: token });
    setToken("");
    try {
      const response = await fetch(`/api/realms/${encodeURIComponent(realm)}/registry-credentials`, {
        method: "POST",
        headers: { Accept: "application/json", "Content-Type": "application/json" },
        body,
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw result.error ?? { code: "http_error", message: `HTTP ${response.status}` };
      setNotice({ kind: "success", message: `Saved credential for ${serverLabel(target)} (user ${result.username ?? username.trim()}).` });
      setServer("");
      setUsername("");
      onSaved();
    } catch (error) {
      const failure = error as Partial<Failure>;
      setNotice({ kind: "error", error: { code: failure.code ?? "unavailable", message: failure.message ?? "Request failed. Try again." } });
    } finally {
      setPending(false);
    }
  }

  return <section className="panel registry-credentials" aria-label="Registry credentials">
    <div className="panel-heading"><h2>Registry credentials</h2><span className="badge">{credentials.length} configured</span></div>
    <div className="panel-body">
      <p className="description">Pull credentials for private registries. Tokens are write-only and never shown; saving an entry for an existing server replaces it.</p>
      {credentials.length ? <table className="data-table"><thead><tr><th scope="col">Server</th><th scope="col">Username</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {credentials.map(cred => <tr key={cred.serverAddress ?? ""}><td>{cred.serverAddress ? <code>{cred.serverAddress}</code> : serverLabel()}</td><td>{cred.username}</td><td><button type="button" aria-label={`Replace credential for ${serverLabel(cred.serverAddress)}`} onClick={() => edit(cred)}>Replace</button></td></tr>)}
      </tbody></table> : <p>No registry credentials are configured for this realm.</p>}
      <form className="registry-credential-form" aria-label="Add or replace registry credential" method="post" autoComplete="off" onSubmit={submit}>
        <label>Server (empty: from image reference)<input name="server" value={server} placeholder="ghcr.io" autoComplete="off" spellCheck={false} onChange={e => setServer(e.target.value)} /></label>
        <label>Username<input ref={usernameInput} name="username" value={username} required autoComplete="off" spellCheck={false} onChange={e => setUsername(e.target.value)} /></label>
        <label>Token<input name="token" type="password" value={token} required autoComplete="new-password" spellCheck={false} onChange={e => setToken(e.target.value)} /></label>
        <button type="submit" disabled={pending}>{pending ? "Saving…" : replacing ? "Replace credential" : "Add credential"}</button>
      </form>
      {notice?.kind === "success" && <p className="creation-success" role="status">{notice.message}</p>}
      {notice?.kind === "error" && <ErrorNotice error={notice.error} />}
    </div>
  </section>;
}
