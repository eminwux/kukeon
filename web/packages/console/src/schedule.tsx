import type { Inventory } from "./inventory";
import { resourceLink } from "./routes";
import { targetScope } from "./connectors.mjs";
import { concurrencyText, deadlineText, scheduleText, validTime, type Schedule, type ScheduleStatus } from "./schedule-format.mjs";

// when renders a timestamp, or "—" for an unset or zero one.
export function when(value?: string) {
  return validTime(value) ? <time dateTime={value}>{new Date(value).toLocaleString()}</time> : "—";
}

// CellName names a cell a schedule fired: a link while the inventory still has it, else plain text
// marked deleted — scheduled cells delete themselves when they end.
function CellName({ name, cellLink }: { name: string; cellLink: (name: string) => string | undefined }) {
  const href = cellLink(name);
  return href ? <a className="resource-name mono-id" href={href}>{name}</a> : <span className="mono-id">{name} <span className="description">(deleted)</span></span>;
}

// cellLinker links the cells a Config's schedule fired: they land in the Config's target scope, and
// only a cell the inventory still has gets a link.
export function cellLinker(inventory: Inventory | undefined, config: { metadata: { realm?: string; space?: string; stack?: string } }): (name: string) => string | undefined {
  const target = targetScope(config.metadata);
  return name => {
    const scope = [target.realm, target.space, target.stack, name];
    return inventory?.rows[3].some(row => JSON.stringify(row.scope) === JSON.stringify(scope)) ? resourceLink(scope) : undefined;
  };
}

// ScheduleDetails is the definition list of one schedule (#1630): its definition from spec.schedule and
// what kukeond's scheduler reports in status.schedule. Shared by a Config's page and the Health page.
// Nothing here edits a schedule. A caller that already badges the state passes state={false}.
export function ScheduleDetails({ schedule, status, cellLink, state = true }: { schedule: Schedule; status?: ScheduleStatus; cellLink: (name: string) => string | undefined; state?: boolean }) {
  const deadline = deadlineText(schedule);
  const active = status?.activeCells ?? [];
  return <dl className="details-grid schedule-details">
    {state && <><dt>State</dt><dd><span className={`state-badge tone-${schedule.suspend ? "stopped" : "ok"}`}><span className="state-dot" aria-hidden="true" />{schedule.suspend ? "Suspended" : "Scheduled"}</span></dd></>}
    <dt>Cron</dt><dd className="mono-id">{scheduleText(schedule)}</dd>
    <dt>Concurrency policy</dt><dd>{concurrencyText(schedule)}</dd>
    {deadline && <><dt>Starting deadline</dt><dd>{deadline}</dd></>}
    <dt>Next fire</dt><dd>{schedule.suspend ? "— (suspended)" : when(status?.nextScheduleTime)}</dd>
    <dt>Last fire</dt><dd>{when(status?.lastScheduleTime)}</dd>
    <dt>Last cell</dt><dd>{status?.lastCell ? <CellName name={status.lastCell} cellLink={cellLink} /> : "—"}</dd>
    <dt>Active cells</dt><dd>{active.length ? active.map((name, i) => <span key={name}>{i > 0 && ", "}<CellName name={name} cellLink={cellLink} /></span>) : "None"}</dd>
    <dt>Skipped fires</dt><dd>{status ? status.skippedFires ?? 0 : "—"}</dd>
    <dt>Last error</dt><dd>{status?.lastError ? <span className="health-last-error">{status.lastError}</span> : "None"}</dd>
  </dl>;
}
