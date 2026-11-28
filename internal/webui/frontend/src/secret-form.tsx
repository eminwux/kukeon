import { useState, type FormEvent } from "react";
import { ErrorNotice, type Failure } from "./request";
import { useNotify } from "./notifications";
import { catalogPageLink } from "./routes";
import { catalogAPI } from "./catalog-url.mjs";
import { ScopeGroup, ScopePicker, SecretMaterialFields, sendJSON, toFailure } from "./connector-ui";
import { clearMaterial, emptySecretForm, scopeLevels, scopeParts, secretErrors, secretPayload, secretTypeOf, secretTypes } from "./connectors.mjs";
import type { IdentityForm as FormState, Scope } from "./connectors.mjs";
import type { InventoryResource } from "./inventory";
import { navigate, routeHref } from "./router";

type SecretDoc = { metadata: { name: string; realm?: string; space?: string; stack?: string; cell?: string }; spec?: { type?: string } };
type SecretResult = { name: string; scope: string[]; type: string; action: string };

// SecretForm creates an untagged Secret (New Secret) or, given a stored one,
// replaces its material (Replace value): same name, scope and type. Material is
// write-only, exactly as in the identity form: held only while typed, sent in
// the POST body, cleared before the request leaves and never rendered back.
export function SecretForm({ scope, existing, onSaved, onCancel }: {
  scope: string[];
  existing?: SecretDoc;
  onSaved: (scope: string[], action: string) => void;
  onCancel?: () => void;
}) {
  const replace = !!existing;
  const [form, setForm] = useState<FormState>(() => emptySecretForm(existing));
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Failure>();
  // A create refused because the name is taken links to the stored Secret.
  const [taken, setTaken] = useState<string[]>();
  const { notify } = useNotify();
  const set = (next: Partial<FormState>) => { setForm(old => ({ ...old, ...next })); setError(undefined); setTaken(undefined); };
  const name = replace ? existing.metadata.name : form.name.trim();
  const target = scope.join(" / ");

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const problems = !scope[0] ? ["Choose where the Secret is stored."] : secretErrors(form, replace);
    if (problems.length) { setError({ code: "invalid_request", message: problems.join(" ") }); return; }
    const body = JSON.stringify(secretPayload(form, replace));
    const path = [...scope, name];
    // The material leaves the form before the request is even sent.
    setForm(old => clearMaterial(old));
    setPending(true); setError(undefined); setTaken(undefined);
    try {
      const result = await sendJSON<SecretResult>(catalogAPI("secrets", path), body);
      notify({ key: `secret:${JSON.stringify(path)}`, tone: "success", message: `Secret ${result.name} ${result.action === "updated" ? "replaced" : "created"} in ${target}.`, link: { href: catalogPageLink("secrets", path), label: "Open Secret" } });
      if (!replace) setForm(emptySecretForm());
      onSaved(path, result.action);
    } catch (caught) {
      const failure = toFailure(caught);
      if (failure.code === "already_exists") setTaken(path);
      setError(failure);
    } finally { setPending(false); }
  }

  return <form className="creation-form identity-form secret-form" aria-label={replace ? `Replace value of secret ${name}` : "New Secret"} method="post" autoComplete="off" onSubmit={submit}>
    <h3>{replace ? `Replace value of ${name}` : "Secret"}</h3>
    <p className="description">{replace
      ? <>Replaces the value stored under <code>{name}</code> in {target}. The name, scope and type stay; only the material changes.</>
      : <>Stored untagged{target && <> in {target}</>}. A Secret for a connector is created from that connector's page.</>} Values are write-only: they are sent once and never shown again.</p>
    {replace && <p className="secret-running-note" role="note">Running cells keep the value they started with. A cell reads the new value when its containers are created again; a restart re-reads only Secrets delivered as environment variables.</p>}
    <div className="schema-fields">
      <label className="schema-field"><span>Name{!replace && <b aria-label="required"> *</b>}</span><input value={replace ? name : form.name} disabled={pending || replace} autoComplete="off" spellCheck={false} onChange={event => set({ name: event.target.value })} /></label>
      <label className="schema-field"><span>Type</span>{replace
        ? <input value={secretTypeOf(existing)} disabled readOnly />
        : <select value={form.type} disabled={pending} onChange={event => set({ type: event.target.value })}>{secretTypes.map(type => <option key={type}>{type}</option>)}</select>}</label>
    </div>
    <SecretMaterialFields type={form.type} form={form} set={set} pending={pending} />
    <div className="connector-actions">
      <button type="submit" disabled={pending}>{pending ? "Saving…" : replace ? "Replace value" : "Create Secret"}</button>
      {onCancel && <button type="button" className="subtle-button" disabled={pending} onClick={onCancel}>Cancel</button>}
    </div>
    {taken ? <div className="error" role="alert"><strong>Secret already exists</strong><p>A Secret named <code>{taken.at(-1)}</code> is already stored in {taken.slice(0, -1).join(" / ")}. Nothing was written. <a href={catalogPageLink("secrets", taken)}>Open it</a> to replace its value.</p></div>
      : error && <ErrorNotice error={error} kind="Secret" />}
  </form>;
}

// NewSecretPage is #/new-secret: where the Secret is stored — a Realm, Space or
// Stack from the inventory — then the form. A cell-scoped Secret is not
// created here, as with `kuke create secret`.
export function NewSecretPage({ inventory, onChanged }: { inventory: InventoryResource; onChanged: () => void }) {
  const [scope, setScope] = useState<Scope>({ realm: "" });
  return <>
    <div className="detail-actions"><a href={routeHref("/secrets")}>← Back to secrets</a></div>
    <section className="panel new-secret-panel" aria-label="New Secret"><div className="panel-body">
      <ScopeGroup title="Stored in" description="The Realm, Space or Stack the Secret belongs to. Cells reference it there through secretRef.">
        <ScopePicker label="Secret scope" inventory={inventory} levels={scopeLevels} value={scope} onChange={setScope} />
      </ScopeGroup>
      <SecretForm key={JSON.stringify(scopeParts(scope))} scope={scopeParts(scope)} onSaved={path => { onChanged(); navigate(catalogPageLink("secrets", path)); }} onCancel={() => { navigate(routeHref("/secrets")); }} />
    </div></section>
  </>;
}
