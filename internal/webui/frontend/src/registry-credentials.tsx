import { useRef, useState, type FormEvent } from "react";
import { ErrorNotice, type Failure } from "./request";
import { useNotify } from "./notifications";
import { RowAction, RowActions } from "./icons";

// RegistryCredential is the redacted realm entry kuke serve returns (#1407,
// #1548). A Secret-backed entry names the realm Secret holding the credential;
// a legacy inline entry names its username. The password is never part of
// either, so it is never read from the document.
export type RegistryCredential = { serverAddress?: string; username?: string; secretRef?: { name: string; realm?: string } };

function serverLabel(server?: string): string {
  return server ? server : "Default (registry from image reference)";
}

// RegistryCredentialsPanel adds or replaces a realm's pull credential by
// server, matching `kuke create registry-credential`: the credential is stored
// as a Secret of the realm and the entry points at it, so saving a legacy
// inline entry again moves it into a Secret. The token is write-only: it is
// held only while the form is filled, sent in the POST body, and cleared after
// every submission.
export function RegistryCredentialsPanel({ realm, credentials, onSaved }: { realm: string; credentials: RegistryCredential[]; onSaved: () => void }) {
  const [server, setServer] = useState("");
  const [username, setUsername] = useState("");
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  const { notify } = useNotify();
  const usernameInput = useRef<HTMLInputElement>(null);
  const replacing = credentials.some(cred => (cred.serverAddress ?? "") === server.trim());

  function edit(cred: RegistryCredential) {
    setServer(cred.serverAddress ?? "");
    setUsername(cred.username ?? "");
    setToken("");
    setError(undefined);
    usernameInput.current?.focus();
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const target = server.trim();
    if (!username.trim() || !token) {
      setError({ code: "invalid_request", message: !username.trim() ? "Username is required." : "Token is required." });
      return;
    }
    setPending(true);
    setError(undefined);
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
      notify({ key: `registry-credential:${realm}`, tone: "success", message: `Saved credential for ${serverLabel(target)} (user ${result.username ?? username.trim()}).` });
      setServer("");
      setUsername("");
      onSaved();
    } catch (caught) {
      const failure = caught as Partial<Failure>;
      setError({ code: failure.code ?? "unavailable", message: failure.message ?? "Request failed. Try again." });
    } finally {
      setPending(false);
    }
  }

  return <section className="panel registry-credentials" aria-label="Registry credentials">
    <div className="panel-heading"><h2>Registry credentials</h2><span className="badge">{credentials.length} configured</span></div>
    <div className="panel-body">
      <p className="description">Pull credentials for private registries. Each is stored as a Secret of the realm; tokens are write-only and never shown. Saving an entry for an existing server replaces it, and moves a legacy inline entry into a Secret.</p>
      {credentials.length ? <table className="data-table"><thead><tr><th scope="col">Server</th><th scope="col">Username</th><th scope="col">Stored in</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {credentials.map(cred => <tr key={cred.serverAddress ?? ""}><td>{cred.serverAddress ? <code>{cred.serverAddress}</code> : serverLabel()}</td><td>{cred.username || <span className="description">In the Secret</span>}</td><td>{cred.secretRef ? <>Secret <a className="resource-name" href={`#/secrets/${encodeURIComponent(cred.secretRef.realm || realm)}/${encodeURIComponent(cred.secretRef.name)}`}>{cred.secretRef.name}</a></> : <span className="badge" title="Save this entry again to move its password into a Secret.">Inline (legacy)</span>}</td><td className="row-actions"><RowActions><RowAction icon="edit" label={`Replace credential for ${serverLabel(cred.serverAddress)}`} title="Replace credential" onClick={() => edit(cred)} /></RowActions></td></tr>)}
      </tbody></table> : <p>No registry credentials are configured for this realm.</p>}
      <form className="registry-credential-form" aria-label="Add or replace registry credential" method="post" autoComplete="off" onSubmit={submit}>
        <label>Server (empty: from image reference)<input name="server" value={server} placeholder="ghcr.io" autoComplete="off" spellCheck={false} onChange={e => setServer(e.target.value)} /></label>
        <label>Username<input ref={usernameInput} name="username" value={username} required autoComplete="off" spellCheck={false} onChange={e => setUsername(e.target.value)} /></label>
        <label>Token<input name="token" type="password" value={token} required autoComplete="new-password" spellCheck={false} onChange={e => setToken(e.target.value)} /></label>
        <button type="submit" disabled={pending}>{pending ? "Saving…" : replacing ? "Replace credential" : "Add credential"}</button>
      </form>
      {error && <ErrorNotice error={error} />}
    </div>
  </section>;
}
