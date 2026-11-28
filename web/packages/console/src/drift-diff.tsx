import { useMemo, useState } from "react";
import { Disclosure } from "@kukeon/ui";
import { useResource } from "./request";
import { apiPath } from "./routes";
import { compact, diffLines, expandFold, hasChanges, restartOutcome, type DiffRow, type DisplayRow } from "./manifest-diff.mjs";

// The drift diff of a Config-made cell (#1768): the stored cell beside the cell its Config renders to now, as
// YAML. The daemon builds the right side from the stored cell with only the fields the drift check reports
// replaced, so the two sides differ exactly where the cell is out of sync. Env values arrive masked.
type Drift = {
  live: string;
  desired?: string;
  configFound: boolean;
  reasons: string[];
  paths: string[];
  changeType?: string;
};

const sideMark = { left: "-", right: "+" } as const;

function DiffCell({ row, side }: { row: DiffRow; side: "left" | "right" }) {
  const text = side === "left" ? row.left : row.right;
  const no = side === "left" ? row.leftNo : row.rightNo;
  const marked = row.kind === "changed" || row.kind === (side === "left" ? "removed" : "added");
  if (text === undefined) return <div className={`drift-cell drift-${side} drift-empty`} aria-hidden="true" />;
  return <div className={`drift-cell drift-${side}${marked ? ` drift-${side === "left" ? "removed" : "added"}` : ""}`}>
    <span className="drift-no" aria-hidden="true">{no}</span>
    <span className="drift-mark">{marked ? sideMark[side] : " "}</span>
    <code>{text}</code>
  </div>;
}

export function DriftDiff({ scope, revision }: { scope: string[]; revision: number }) {
  const [open, setOpen] = useState(false);
  const [full, setFull] = useState(false);
  const [expanded, setExpanded] = useState<{ source: DisplayRow[]; rows: DisplayRow[] } | null>(null);
  const drift = useResource<Drift>(open ? `${apiPath(scope)}/drift` : null, revision);
  const data = drift.data;
  const allRows = useMemo(() => data?.desired === undefined ? [] : diffLines(data.live, data.desired), [data]);
  const compactRows = useMemo(() => compact(allRows), [allRows]);
  // A fold opened by a click stays open until the diff itself changes.
  const rows = full ? allRows : expanded?.source === compactRows ? expanded.rows : compactRows;
  const unfold = (index: number) => setExpanded({ source: compactRows, rows: expandFold(rows, index) });

  let body;
  if (drift.error) body = <p className="drift-note" role="alert">{drift.error.message}</p>;
  else if (!data) body = <p className="drift-note" role="status">Loading diff…</p>;
  else if (!data.configFound || data.desired === undefined) body = <p className="drift-note">{data.reasons[0] ?? "lineage Config deleted"}</p>;
  else body = <>
    <div className="drift-toolbar">
      <p className="drift-outcome">{restartOutcome(data.changeType)}</p>
      <button type="button" role="switch" aria-checked={full} onClick={() => setFull(!full)}>
        <span className="switch-track" aria-hidden="true"><span className="switch-thumb" /></span>
        <span>Full manifest</span>
      </button>
    </div>
    {!hasChanges(allRows) && <p className="drift-note">The two manifests are identical; only the reasons above differ.</p>}
    <div className="drift-grid" aria-label="Manifest diff">
      <div className="drift-head drift-left">Running cell</div>
      <div className="drift-head drift-right">From Config</div>
      {rows.map((row, i) => row.kind === "fold"
        ? [<button type="button" className="drift-fold drift-left" key={`${i}-l`} onClick={() => unfold(i)}>{row.count} unchanged {row.count === 1 ? "line" : "lines"}</button>,
          <button type="button" className="drift-fold drift-right drift-fold-echo" key={`${i}-r`} tabIndex={-1} aria-hidden="true" onClick={() => unfold(i)}>{row.count} unchanged {row.count === 1 ? "line" : "lines"}</button>]
        : [<DiffCell row={row} side="left" key={`${i}-l`} />, <DiffCell row={row} side="right" key={`${i}-r`} />])}
    </div>
  </>;

  return <Disclosure title="Show diff" level={3} className="drift-diff" open={open} onToggle={setOpen}>
    <div className="drift-body">{body}</div>
  </Disclosure>;
}
