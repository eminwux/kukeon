import { useEffect, useRef, useState } from "react";
import { displayNameError } from "./cells.mjs";
import { apiPath } from "./routes";
import type { ConfigRef } from "./inventory";
import { apiFetch } from "./transport";

// The display names of #1692: the kukeon.io/display-name annotation on an agent's Config or on one of its
// cells. Setting or clearing one never restarts, recreates or marks a cell OutOfSync; the daemon writes
// the annotation only.

type Annotations = Record<string, string> | undefined;

async function post(path: string, body: unknown): Promise<Record<string, unknown>> {
  const response = await apiFetch(path, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body) });
  let result: { error?: { message?: string } } & Record<string, unknown> = {};
  try { result = await response.json(); } catch { /* Use the HTTP status below. */ }
  if (!response.ok) throw new Error(result.error?.message ?? `Request failed (HTTP ${response.status})`);
  return result;
}

function annotationsOf(value: unknown): Annotations {
  const annotations = (value as { annotations?: unknown } | undefined)?.annotations;
  return annotations && typeof annotations === "object" ? annotations as Record<string, string> : undefined;
}

// renameConfig sets (or, with "", clears) an agent's display name; it returns the Config's annotations.
export async function renameConfig(config: ConfigRef, displayName: string): Promise<Annotations> {
  const result = await post("/configs/display-name", { config: { name: config.name, realm: config.realm, ...(config.space ? { space: config.space } : {}), ...(config.stack ? { stack: config.stack } : {}) }, displayName });
  return annotationsOf(result.config);
}

// renameCell sets (or, with "", clears) one instance's display name; it returns the cell's annotations.
export async function renameCell(scope: string[], displayName: string): Promise<Annotations> {
  const result = await post(`${apiPath(scope)}/display-name`, { displayName });
  return annotationsOf(result.cell);
}

// RenameControl is the Rename action of an agent or an instance: a button that opens an inline form.
// Saving an empty name clears it, so the default label comes back.
export function RenameControl({ subject, current, onSave }: { subject: string; current: string; onSave: (displayName: string) => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(current);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => { if (editing) input.current?.focus(); }, [editing]);
  if (!editing) return <button type="button" className="subtle-button rename-action" aria-label={`Rename ${subject}`} onClick={() => { setValue(current); setError(""); setEditing(true); }}>Rename</button>;
  const invalid = displayNameError(value);
  async function save() {
    if (pending || invalid) return;
    setPending(true); setError("");
    try { await onSave(value.trim()); setEditing(false); }
    catch (failure) { setError(failure instanceof Error ? failure.message : "The request could not reach the web server."); }
    finally { setPending(false); }
  }
  return <form className="rename-form" aria-label={`Rename ${subject}`} onSubmit={event => { event.preventDefault(); void save(); }}>
    <label>Display name<input ref={input} type="text" value={value} maxLength={63} placeholder="Leave empty to clear" onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === "Escape") setEditing(false); }} /></label>
    <button type="submit" className="primary-action" disabled={pending || !!invalid}>{pending ? "Saving…" : "Save"}</button>
    <button type="button" className="subtle-button" disabled={pending} onClick={() => setEditing(false)}>Cancel</button>
    {(invalid || error) && <p className="error" role="alert">{invalid || error}</p>}
  </form>;
}
