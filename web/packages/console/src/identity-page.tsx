import { Fragment, useState } from "react";
import { useNotify } from "./notifications";
import type { InventoryResource } from "./inventory";
import { CatalogDeleteConfirmation } from "./catalog";
import { catalogDeleteAPI } from "./catalog-url.mjs";
import { LoginButton, LoginStateBadge, identitiesLink, rotateIdentityLink, sendJSON, toFailure, volumeLink } from "./connector-ui";
import { defaultTargets, deliveryText, refLabel, refScope } from "./connectors.mjs";
import type { Connector } from "./connectors.mjs";
import { navigate, routeHref } from "./router";
import { catalogPageLink, configPageLink } from "./routes";
import { defaultLabel, kindLogin, pageOf, scopeLabel, usedByCount } from "./identities.mjs";
import type { IdentityRow, IdentityUser, SystemDefault } from "./identities.mjs";

// The identity page (#1805): one identity of the Identities list — a Secret tagged with a connector or a
// login identity — built from the row the list computes, with no request of its own. Only names, scopes,
// types, readable fields and whether a login is present are shown, as on the list.

function userScope(user: IdentityUser): string[] { return [user.realm, user.space, user.stack].filter((value): value is string => !!value); }

// deliveries words how the identity reaches a cell: the login method's delivery, or one line per method
// of the connector that takes a Secret.
function deliveries(connector: Connector | undefined, row: IdentityRow): { method: string; text: string }[] {
  const methods = connector?.methods ?? [];
  if (row.kind === kindLogin) {
    const method = methods.find(item => item.name === row.method);
    return [{ method: row.method, text: deliveryText(method ?? { delivery: "stateVolume" }) }];
  }
  return defaultTargets(connector).map(target => ({ method: target.method, text: deliveryText({ delivery: target.delivery, ...methods.find(method => method.name === target.method) }) }));
}

// UsedByTable lists the Configs using an identity, linked, in pages of 25.
function UsedByTable({ row }: { row: IdentityRow }) {
  const [page, setPage] = useState(0);
  const { configs, teams } = usedByCount(row);
  const shown = pageOf(row.usedBy, page);
  return <section className="panel identity-used-by-panel" aria-label="Used by">
    <div className="panel-heading"><h2>Used by</h2><span className="badge">{configs} {configs === 1 ? "Config" : "Configs"} · {teams.length} {teams.length === 1 ? "team" : "teams"}</span></div>
    <div className="panel-body">
      {configs ? <>
        <div className="table-scroll"><table aria-label="Configs using this identity"><thead><tr><th scope="col">Config</th><th scope="col">Role</th><th scope="col">Scope</th></tr></thead>
          <tbody>{shown.items.map(user => <tr key={[...userScope(user), user.name].join("/")}>
            <td><a className="resource-name" href={configPageLink(userScope(user), user.name)}>{user.name}</a></td>
            <td>{user.role ? <code>{user.role}</code> : <span className="description">—</span>}</td>
            <td className="scope-cell">{userScope(user).join(" / ")}</td>
          </tr>)}</tbody>
        </table></div>
        {shown.total > 25 && <div className="pagination"><span>{`${shown.first}–${shown.end} of ${shown.total}`}</span><button type="button" disabled={!shown.page} onClick={() => setPage(shown.page - 1)}>Previous</button><button type="button" disabled={shown.page >= shown.last} onClick={() => setPage(shown.page + 1)}>Next</button></div>}
      </> : <p className="description">No Config uses this identity.</p>}
    </div>
  </section>;
}

export function IdentityDetailPage({ connector, row, pinned, loading, wanted, revision, inventory, onChanged }: {
  connector: Connector | undefined;
  row: IdentityRow | undefined;
  pinned: SystemDefault[];
  loading: boolean;
  wanted: string[] | undefined;
  revision: number;
  inventory: InventoryResource;
  onChanged: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const { notify } = useNotify();
  const back = identitiesLink();
  if (!row) {
    if (loading) return <p role="status">Loading identity…</p>;
    const name = wanted?.at(-1);
    return <>
      <div className="detail-actions"><a href={back}>← Back to Identities</a></div>
      <div className="error" role="alert"><strong>Identity not found</strong><p>No {connector?.title || connector?.name || ""} identity {name ? <code>{name}</code> : ""} is stored{wanted && wanted.length > 1 ? <> in {wanted.slice(0, -1).join(" / ")}</> : ""}. <a href={back}>Back to Identities</a></p></div>
    </>;
  }
  const login = row.kind === kindLogin;
  const noun = `${row.title} ${login ? "login identity" : "identity"}`;
  const label = `${noun} ${refLabel(row.ref)}`;
  const fields = Object.entries(row.fields);
  async function remove() {
    if (!row || pending) return;
    setPending(true); setError("");
    try {
      await sendJSON(catalogDeleteAPI(login ? "volumes" : "secrets", [...refScope(row.ref), row.ref.name]), "{}");
      notify({ key: `identity-delete:${row.key}`, tone: "success", message: `${noun} ${row.name} deleted.` });
      onChanged();
      navigate(back);
    } catch (caught) { setError(toFailure(caught).message); onChanged(); }
    finally { setPending(false); }
  }
  return <>
    <div className="detail-actions"><a href={back}>← Back to Identities</a></div>
    <section className="panel identity-page" aria-label={label}>
      <div className="panel-heading"><h2>{row.name}</h2>
        {pinned.length > 0 && <span className="badge system-default-badge">System default</span>}
        {row.unused && <span className="badge identity-unused" title="No Config and no default uses it: it can be deleted.">Unused</span>}
        {login && !row.exists && <span className="badge">Not created yet</span>}
        {row.session && <span className="badge">Login running</span>}
      </div>
      <div className="panel-body">
        <p className="description">{login ? "A login identity: the Volume the harness keeps its login in. Only whether a login is present is shown — never the login itself." : "A Secret tagged with a connector. Only its name, scope, type and readable fields are shown — never its value."}</p>
        <div className="connector-actions identity-actions">
          {login ? <LoginButton connector={row.connector} title={row.title} volume={row.ref} state={row.status} running={!!row.session} method={row.method || undefined} onStarted={onChanged} />
            : <a className="subtle-button" href={rotateIdentityLink(row.connector, row.ref)} aria-label={`Rotate ${label}`}>Rotate</a>}
          {row.exists && <button type="button" className="subtle-button danger-action" aria-label={`Delete ${label}`} disabled={pending} onClick={() => setConfirming(true)}>{pending ? "Deleting…" : "Delete"}</button>}
          <a className="subtle-button" href={routeHref("/identities/assignments")}>Assignments</a>
        </div>
        {error && <div className="error" role="alert"><strong>Delete failed: {label}</strong><p>{error}</p></div>}
        <dl className="details-grid" aria-label="Identity details">
          <dt>Connector</dt><dd><a href={identitiesLink(row.connector)}>{row.title}</a></dd>
          <dt>{login ? "Method" : "Secret type"}</dt><dd><code>{login ? row.method || "—" : row.type}</code></dd>
          <dt>Delivered as</dt><dd>{deliveries(connector, row).map(item => <div key={item.method || "-"}>{!login && item.method && <><code>{item.method}</code>: </>}{item.text}</div>)}</dd>
          <dt>Scope</dt><dd className="scope-cell">{scopeLabel(row)}</dd>
          {login && <><dt>Login</dt><dd><LoginStateBadge state={row.status} />{row.since && <span className="description"> since {new Date(row.since).toLocaleString()}</span>}</dd></>}
          <dt>Stored in</dt><dd>{login ? row.exists ? <a href={volumeLink(row.ref)}>Volume <code>{row.ref.name}</code></a> : <span className="description">Not created yet: Log in, or the first cell start of a Config using it, creates the Volume <code>{row.ref.name}</code>.</span>
            : <a href={catalogPageLink("secrets", [...refScope(row.ref), row.ref.name])}>Secret <code>{row.ref.name}</code></a>}</dd>
        </dl>
      </div>
    </section>
    {fields.length > 0 && <section className="panel" aria-label="Fields">
      <div className="panel-heading"><h2>Fields</h2></div>
      <div className="panel-body"><dl className="details-grid">{fields.map(([name, value]) => <Fragment key={name}><dt>{name}</dt><dd><code>{value}</code></dd></Fragment>)}</dl></div>
    </section>}
    <section className="panel" aria-label="Defaults">
      <div className="panel-heading"><h2>Defaults</h2></div>
      <div className="panel-body">{pinned.length || row.defaults.length ? <ul className="state-list">
        {pinned.map(item => <li key={item.key}><div><span className="badge system-default-badge">System default</span> for {item.title}{item.method && <> <code>{item.method}</code></>}</div></li>)}
        {row.defaults.map(item => <li key={`${item.level}:${item.scope.join("/")}:${item.slot}`}><div>{defaultLabel(item)}{item.slot ? <span className="description"> · slot <code>{item.slot}</code></span> : item.git ? <span className="description"> · git identity</span> : null}</div></li>)}
      </ul> : <p className="description">No default names this identity.</p>}</div>
    </section>
    <section className="panel" aria-label="Roles">
      <div className="panel-heading"><h2>Roles</h2></div>
      <div className="panel-body">{row.roles.length ? <p>{row.roles.map(role => <span className="badge label-badge" key={role}>{role}</span>)}</p> : <p className="description">No team role uses this identity.</p>}</div>
    </section>
    <UsedByTable row={row} />
    {confirming && <CatalogDeleteConfirmation kind={login ? "volumes" : "secrets"} doc={{ metadata: { name: row.ref.name, realm: row.ref.realm, ...(row.ref.space ? { space: row.ref.space } : {}), ...(row.ref.stack ? { stack: row.ref.stack } : {}) } }} revision={revision} inventory={inventory} onCancel={() => setConfirming(false)} onConfirm={() => { setConfirming(false); void remove(); }} />}
  </>;
}
