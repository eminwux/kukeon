import { useEffect, useState } from "react";
import { systemTeam, teamsLink } from "./teams.mjs";
import { useResource, ErrorNotice } from "./request";
import { AskMajordomo, configScope, oneLine, sweepTask, type ConfigDoc, type Majordomo } from "./majordomo";
import { ScheduleDetails, cellLinker, when } from "./schedule";
import type { InventoryResource } from "./inventory";

type Finding = {
  signature: string; classification: string; severity: string; recurring?: boolean; title?: string; count?: number;
  evidence?: string[]; suggestedCommands?: string[]; issueUrl?: string;
};
type Report = {
  id: string; createdAt: string; since: string; until: string; summary: string; body?: string;
  cell?: { realm: string; space: string; stack: string; name: string }; findings: Finding[];
};
type Summary = { id: string; createdAt: string; since: string; until: string; summary: string; findings: number; recurring: number; filed: number };

function configLink(scope: string[]): string { return "#/configs/" + scope.map(encodeURIComponent).join("/"); }
const classTone: Record<string, string> = { kukeon: "error", workload: "warning", unclear: "neutral" };

function FindingItem({ finding }: { finding: Finding }) {
  return <li className="health-finding">
    <div className="health-finding-heading">
      <strong>{finding.title || finding.signature}</strong>
      <span className={`state-badge tone-${classTone[finding.classification] ?? "neutral"}`} title="Classification"><span className="state-dot" aria-hidden="true" />{finding.classification}</span>
      <span className="badge" title="Severity">{finding.severity}</span>
      <span className="badge" title="Recurrence">{finding.recurring ? "Recurring" : "New"}</span>
      {finding.issueUrl && <a className="text-link" href={finding.issueUrl} target="_blank" rel="noopener noreferrer">Filed issue</a>}
    </div>
    {finding.title && <p className="mono-id health-signature">{finding.signature}</p>}
    {!!finding.evidence?.length && <details><summary>Evidence ({finding.evidence.length})</summary>{finding.evidence.map((entry, i) => <pre key={i} tabIndex={0}>{entry}</pre>)}</details>}
    {!!finding.suggestedCommands?.length && <div className="health-commands"><span>Suggested commands (not run):</span><ul>{finding.suggestedCommands.map((command, i) => <li key={i}><code>{command}</code></li>)}</ul></div>}
  </li>;
}

function reportQuestion(report: Report): string {
  const kukeon = report.findings.filter(finding => finding.classification === "kukeon").length;
  return oneLine(`About health report ${report.id} (created ${report.createdAt}, window ${report.since} to ${report.until}, ${report.findings.length} findings, ${kukeon} classified kukeon): read it with "kuke health report ${report.id}" and walk me through what needs attention first.`);
}

// Suspended sweeps are turned on in the kuke team's stored declaration (#1568, #1618): the Teams page
// edits and reapplies it. Schedules are never edited from this page.
function EnableSweep({ configs }: { configs: ConfigDoc[] }) {
  const edit = `# under spec.roles[] for the majordomo role\ntasks:\n  - { name: ${sweepTask}, suspend: false }`;
  const names = configs.map(config => config.metadata.name);
  return <div className="health-enable" role="note">
    <strong>Sweeps are suspended.</strong>
    <p>{names.length === 1 ? `${names[0]} ships` : `These ${names.length} sweep configs ship`} switched off so no install spends model credits unasked. To turn sweeps on, edit the <a href={teamsLink(systemTeam)}>kuke team</a>'s declaration on the Teams page, preview and apply it:</p>
    {names.length > 1 && <ul className="health-suspended" aria-label="Suspended sweeps">{names.map((name, index) => <li key={index} className="mono-id">{name}</li>)}</ul>}
    <pre tabIndex={0}>{edit}</pre>
    <p className="description">The schedule cannot be changed from this page.</p>
  </div>;
}

function SweepSchedule({ config, onRun, running, inventory }: { config: ConfigDoc; onRun: (config: ConfigDoc) => void; running: boolean; inventory: InventoryResource }) {
  const schedule = config.spec?.schedule;
  const status = config.status?.schedule;
  const scope = [...configScope(config), config.metadata.name];
  return <li className="health-sweep">
    <div className="health-sweep-heading">
      <a className="resource-name" href={configLink(scope)}>{config.metadata.name}</a>
      <span className={`state-badge tone-${!schedule ? "neutral" : schedule.suspend ? "stopped" : "ok"}`}><span className="state-dot" aria-hidden="true" />{!schedule ? "No schedule" : schedule.suspend ? "Suspended" : "Scheduled"}</span>
      <button type="button" disabled={running} aria-busy={running || undefined} aria-label={`Run sweep now with ${config.metadata.name}`} onClick={() => onRun(config)}>{running ? "Starting sweep…" : "Run sweep now"}</button>
    </div>
    {schedule ? <ScheduleDetails schedule={schedule} status={status} cellLink={cellLinker(inventory.data, config)} /> : <p className="description">This Config has no schedule.</p>}
  </li>;
}

export function HealthPage({ revision, major, inventory, onChanged }: { revision: number; major: Majordomo; inventory: InventoryResource; onChanged: () => void }) {
  const [reads, setReads] = useState(0);
  const [selected, setSelected] = useState("latest");
  const [running, setRunning] = useState("");
  const [sweepResult, setSweepResult] = useState<{ tone: "success" | "error"; message: string } | null>(null);
  const list = useResource<Summary[]>("/api/health/reports", revision + reads);
  const hasReports = !!list.data?.length;
  const report = useResource<Report>(hasReports ? `/api/health/reports/${encodeURIComponent(selected)}` : null, revision + reads);
  // A report dropped by retention falls back to the newest one.
  useEffect(() => {
    if (selected !== "latest" && list.data && !list.data.some(item => item.id === selected)) setSelected("latest");
  }, [list.data, selected]);

  async function runSweep(config: ConfigDoc) {
    if (running) return;
    setRunning(config.metadata.name); setSweepResult(null);
    try {
      const response = await fetch("/api/health/sweep", {
        method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" },
        body: JSON.stringify({ config: { name: config.metadata.name, realm: config.metadata.realm, space: config.metadata.space, stack: config.metadata.stack } }),
      });
      let body: { error?: { message?: string }; cell?: { metadata?: { name?: string } } } = {};
      try { body = await response.json(); } catch { /* Use the HTTP status below. */ }
      if (!response.ok) throw new Error(body.error?.message ?? `HTTP ${response.status}`);
      setSweepResult({ tone: "success", message: `Sweep started in cell ${body.cell?.metadata?.name ?? "(name not reported)"}. It submits a report only if it finds something; refresh to see it.` });
    } catch (error) {
      setSweepResult({ tone: "error", message: error instanceof Error ? error.message : "The request could not reach the web server." });
    } finally { setRunning(""); onChanged(); }
  }

  const current = hasReports ? report.data : undefined;
  const suspended = major.sweeps.filter(config => config.spec?.schedule?.suspend);
  return <>
    <section className="panel health-schedule" aria-label="Sweep schedule">
      <div className="panel-heading"><h2>Sweep schedule</h2><span className="badge">From the task config</span></div>
      <div className="panel-body">
        {major.configs.loading && !major.configs.data && <p role="status">Loading sweep configs…</p>}
        {major.configs.error && <ErrorNotice error={major.configs.error} kind="Configs" />}
        {major.configs.data && !major.sweeps.length && <p className="description" role="status">No health-sweep config found. Run <code>kuke team init</code> on the host to apply majordomo and its sweep task.</p>}
        {suspended.length > 0 && <EnableSweep configs={suspended} />}
        <ul className="health-sweeps">{major.sweeps.map(config => <SweepSchedule key={JSON.stringify([...configScope(config), config.metadata.name])} config={config} onRun={runSweep} running={running === config.metadata.name} inventory={inventory} />)}</ul>
        {sweepResult && <div className={sweepResult.tone === "error" ? "error" : "health-sweep-started"} role={sweepResult.tone === "error" ? "alert" : "status"}>{sweepResult.tone === "error" && <strong>Sweep did not start</strong>}<p>{sweepResult.message}</p></div>}
      </div>
    </section>

    <section className="panel health-report" aria-label="Health report" aria-busy={report.loading || list.loading}>
      <div className="panel-heading"><h2>{selected === "latest" ? "Latest report" : "Report"}</h2><div className="health-report-actions">{current && <AskMajordomo question={reportQuestion(current)} subject={`health report ${current.id}`} />}<button type="button" onClick={() => setReads(n => n + 1)} disabled={list.loading}>{list.loading ? "Refreshing…" : "Refresh"}</button></div></div>
      <div className="panel-body">
        {list.error && <ErrorNotice error={list.error} kind="Health reports" />}
        {report.error && hasReports && <ErrorNotice error={report.error} kind="Health report" />}
        {list.data && !hasReports && <p className="description" role="status">No health report is stored. A sweep submits one only when kukeond's health digest has findings.</p>}
        {current && <>
          <dl className="details-grid">
            <dt>Report</dt><dd className="mono-id">{current.id}</dd>
            <dt>Created</dt><dd>{when(current.createdAt)}</dd>
            <dt>Window</dt><dd>{when(current.since)} to {when(current.until)}</dd>
            <dt>Submitted by</dt><dd className="mono-id">{current.cell ? `${current.cell.realm} / ${current.cell.space} / ${current.cell.stack} / ${current.cell.name}` : "—"}</dd>
          </dl>
          <p className="health-summary">{current.summary}</p>
          {current.findings.length ? <ul className="health-findings" aria-label="Findings">{current.findings.map(finding => <FindingItem key={finding.signature} finding={finding} />)}</ul> : <p className="description">This report has no findings.</p>}
          {current.body && <details className="health-body"><summary>Report text</summary><pre tabIndex={0}>{current.body}</pre></details>}
        </>}
      </div>
    </section>

    <section className="panel" aria-label="Report history">
      <div className="panel-heading"><h2>History</h2><span className="badge">{list.data ? `${list.data.length} stored` : "…"}</span></div>
      <div className="table-scroll"><table><thead><tr><th>Created</th><th>Findings</th><th>Recurring</th><th>Filed</th><th>Summary</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {list.data?.map((item, index) => { const shown = selected === item.id || selected === "latest" && index === 0; return <tr key={item.id} aria-current={shown ? "true" : undefined}>
          <td>{when(item.createdAt)}</td><td>{item.findings}</td><td>{item.recurring}</td><td>{item.filed}</td><td>{item.summary}</td>
          <td className="row-actions"><button type="button" className="text-button" disabled={shown} aria-label={`Show report ${item.id}`} onClick={() => setSelected(item.id)}>{shown ? "Shown" : "Show"}</button></td>
        </tr>; })}
      </tbody></table></div>
      {list.data && !hasReports && <div className="empty-state"><h3>No reports yet</h3></div>}
    </section>
  </>;
}
