import { useEffect, useRef, useSyncExternalStore } from "react";
import type { Failure } from "./request";
import { useNotify } from "./notifications";
import { sendJSON, toFailure } from "./connector-ui";
import { systemTeam, teamsAPI } from "./teams.mjs";

// Initialize from the web UI (#1694): POST /api/teams/init runs `kuke team init`
// with the server's client, as the account running `kuke serve`. Every empty
// state that used to print the command shows the same prompt, and all of them
// share one run: its progress, a 409's reapply offer and its failure show
// wherever a prompt is on screen. `kuke init` itself stays a CLI and installer
// step: it creates the daemon this page talks to.

export type InitTeam = { team: string; resources: number; configs: string[]; blueprints: string[]; missingSecrets: string[]; notes: string[] };
export type InitResult = { teams: InitTeam[]; notes: string[] };

// Error codes of POST /api/teams/init the prompt branches on.
export const codeInitialized = "already_initialized";
export const codeGitMissing = "git_missing";
export const initAPI = "/teams/init";

type Phase = "idle" | "pending" | "conflict" | "reapplying" | "done";
type InitState = { phase: Phase; error?: Failure; result?: InitResult; completed: number };

let state: InitState = { phase: "idle", completed: 0 };
const listeners = new Set<() => void>();
function update(next: Partial<InitState>) {
  state = { ...state, ...next };
  for (const listener of listeners) listener();
}
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

// initSummary words what an init stored, for its toast and status line.
export function initSummary(result: InitResult): string {
  if (!result.teams.length) return "Nothing was applied.";
  // Teams share blueprints, so names are counted once.
  const count = (key: "configs" | "blueprints") => new Set(result.teams.flatMap(team => team[key])).size;
  const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`;
  const names = result.teams.map(team => team.team);
  return `Stored ${names.length === 1 ? "team" : "teams"} ${names.join(" and ")}: ${plural(count("configs"), "Config")}, ${plural(count("blueprints"), "Blueprint")}.`;
}

// initialize runs one init; a second call while one is running joins it.
let running: Promise<InitResult | undefined> | undefined;
function initialize(): Promise<InitResult | undefined> {
  if (running) return running;
  update({ phase: "pending", error: undefined, result: undefined });
  running = (async () => {
    try {
      const result = await sendJSON<InitResult>(initAPI, {});
      update({ phase: "done", result, completed: state.completed + 1 });
      return result;
    } catch (caught) {
      const error = toFailure(caught);
      update({ phase: error.code === codeInitialized ? "conflict" : "idle", error });
      return undefined;
    } finally { running = undefined; }
  })();
  return running;
}

// reapply is the offer of a 409: the existing reapply route of the kuke team.
async function reapply(): Promise<boolean> {
  if (state.phase !== "conflict") return false;
  update({ phase: "reapplying", error: undefined });
  try {
    await sendJSON(teamsAPI(systemTeam, "reapply"), {});
    update({ phase: "done", result: undefined, completed: state.completed + 1 });
    return true;
  } catch (caught) {
    update({ phase: "conflict", error: toFailure(caught) });
    return false;
  }
}

// InitializePrompt is the Initialize button with the CLI command as secondary
// text. compact is the sidebar's form: the button and its status only.
// onChanged re-reads the inventory and the catalog once an init or a reapply
// finished, whichever prompt started it.
export function InitializePrompt({ onChanged, compact = false, subject = "the kuke and default teams" }: { onChanged: () => void; compact?: boolean; subject?: string }) {
  const current = useSyncExternalStore(subscribe, () => state);
  const { notify } = useNotify();
  const seen = useRef(current.completed);
  useEffect(() => {
    if (seen.current === current.completed) return;
    seen.current = current.completed;
    onChanged();
  }, [current.completed, onChanged]);
  async function run() {
    const result = await initialize();
    if (result) notify({ key: "team-init", tone: "success", message: `Initialized. ${initSummary(result)}` });
  }
  async function again() {
    if (await reapply()) notify({ key: "team-init", tone: "success", message: `Team ${systemTeam} reapplied.` });
  }
  const { phase, error, result } = current;
  const conflict = phase === "conflict" || phase === "reapplying";
  const notes = result ? [...result.notes, ...result.teams.flatMap(team => [...team.missingSecrets, ...team.notes])] : [];
  return <div className={`initialize-prompt${compact ? " is-compact" : ""}`}>
    {!conflict && phase !== "done" && <button type="button" className="primary-action initialize-button" disabled={phase === "pending"} aria-busy={phase === "pending" || undefined} title={`Runs kuke team init: stores ${subject}`} onClick={() => void run()}>Initialize</button>}
    {phase === "pending" && <p className="initialize-status" role="status">Initializing… fetching kukeon source</p>}
    {conflict && <div className="initialize-conflict" role="group" aria-label="Already initialized">
      <p><strong>Already initialized.</strong> The kuke team is stored; reapply recomposes it from its stored declaration.</p>
      <button type="button" className="initialize-button" disabled={phase === "reapplying"} aria-busy={phase === "reapplying" || undefined} onClick={() => void again()}>{phase === "reapplying" ? "Reapplying…" : "Reapply"}</button>
    </div>}
    {phase === "done" && <p className="initialize-status" role="status">{result ? `Initialized. ${initSummary(result)}` : `Team ${systemTeam} reapplied.`}</p>}
    {phase === "done" && !compact && notes.length > 0 && <ul className="initialize-notes">{notes.map(note => <li key={note}>{note}</li>)}</ul>}
    {error && phase !== "pending" && phase !== "reapplying" && (phase !== "conflict" || error.code !== codeInitialized) && <div className="error" role="alert"><strong>{error.code === codeGitMissing ? "git is not installed" : conflict ? "Reapply failed" : "Initialization failed"}</strong><p>{error.message}</p></div>}
    {!compact && phase !== "done" && <p className="description initialize-cli">Or run <code>{conflict ? "kuke team init --reapply" : "kuke team init"}</code> on the host.</p>}
  </div>;
}
