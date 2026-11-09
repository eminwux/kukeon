import { useState } from "react";

type Config = { name: string; realm?: string; space?: string; stack?: string };
type Cell = { metadata: { name: string }; spec: { realmId: string; spaceId: string; stackId: string } };
export function CreateCell({ config, onCreated }: { config: Config; onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [env, setEnv] = useState("");
  const [ignore, setIgnore] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<{ document: Cell; preview: string }>();
  async function submit(create: boolean) {
    setPending(true); setError("");
    try {
      const response = await fetch(`/api/cells/from-config/${create ? "create" : "preview"}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ config: { name: config.name, realm: config.realm, space: config.space, stack: config.stack }, name, env: env.split("\n").filter(Boolean), ignoreDiskPressure: ignore, preview: preview?.preview ?? "" }) });
      const result = await response.json();
      if (!response.ok) throw new Error(result.message ?? result.error?.message ?? "Request failed");
      if (create) {
        const cell = result as Cell;
        onCreated();
        window.location.hash = "#/resource/" + [cell.spec.realmId, cell.spec.spaceId, cell.spec.stackId, cell.metadata.name].map(encodeURIComponent).join("/");
      } else setPreview(result);
    } catch (failure) { setError((failure as Error).message); if (create) setPreview(undefined); }
    finally { setPending(false); }
  }
  if (!open) return <button onClick={() => setOpen(true)}>Create cell from config</button>;
  return <section className="panel" aria-label="Create cell from config"><div className="panel-heading"><h2>Create cell from config</h2></div><form className="panel-body create-cell-form" onSubmit={event => { event.preventDefault(); void submit(false); }}>
    <p>Config: <strong>{config.name}</strong>. Creates a stopped cell using the config’s blueprint and values.</p>
    <label>Target scope<select aria-label="Target scope" disabled={pending}><option>{config.realm} / {config.space || "default"} / {config.stack || "default"}</option></select></label>
    <p className="description">The config fixes the compatible target scope, matching the CLI. The stack must already exist.</p>
    <label>Cell name<input required value={name} disabled={pending} onChange={event => { setName(event.target.value); setPreview(undefined); }} /></label>
    <label>Environment overrides<textarea placeholder="KEY=VALUE, one per line" value={env} disabled={pending} onChange={event => { setEnv(event.target.value); setPreview(undefined); }} /></label>
    <label><input type="checkbox" checked={ignore} disabled={pending} onChange={event => { setIgnore(event.target.checked); setPreview(undefined); }} /> Ignore disk pressure for this creation</label>
    <p className="description">Environment overrides are persisted in the cell. Do not enter secret values; use the config’s secret references.</p>
    {error && <p className="error" role="alert">{error}</p>}
    {pending && <p role="status">{preview ? "Creating cell…" : "Preparing preview…"}</p>}
    {preview && <><h3>Review cell document</h3><pre tabIndex={0}>{JSON.stringify(preview.document, null, 2)}</pre><p>Ignore disk pressure: {ignore ? "Yes" : "No"}</p><button type="button" disabled={pending} onClick={() => void submit(true)}>Confirm create cell</button></>}
    <button type="submit" disabled={pending || !name.trim()}>Preview cell</button> <button type="button" disabled={pending} onClick={() => { setOpen(false); setPreview(undefined); }}>Cancel</button>
  </form></section>;
}
