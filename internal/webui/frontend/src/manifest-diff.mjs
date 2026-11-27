// Line diff of two manifests for the cell drift view (#1768): the stored cell on the left, the cell its Config
// renders to now on the right. Manifests are a few hundred lines, so a plain longest-common-subsequence table is
// fast enough and needs no dependency.

function lines(text) {
  if (!text) return [];
  const out = text.split("\n");
  if (out[out.length - 1] === "") out.pop();
  return out;
}

// diffLines aligns the lines of left and right into rows: "same" lines appear on both sides, a run of removed
// lines followed by added lines pairs up as "changed" rows, and what is left over is "removed" (left only) or
// "added" (right only). Line numbers are 1-based.
export function diffLines(left, right) {
  const a = lines(left);
  const b = lines(right);
  const n = a.length;
  const m = b.length;
  // lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
  const lcs = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }

  const rows = [];
  let removed = [];
  let added = [];
  const flush = () => {
    const paired = Math.min(removed.length, added.length);
    for (let k = 0; k < paired; k++) rows.push({ kind: "changed", left: removed[k].text, leftNo: removed[k].no, right: added[k].text, rightNo: added[k].no });
    for (const line of removed.slice(paired)) rows.push({ kind: "removed", left: line.text, leftNo: line.no });
    for (const line of added.slice(paired)) rows.push({ kind: "added", right: line.text, rightNo: line.no });
    removed = [];
    added = [];
  };
  let i = 0;
  let j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && a[i] === b[j]) {
      flush();
      rows.push({ kind: "same", left: a[i], leftNo: i + 1, right: b[j], rightNo: j + 1 });
      i++;
      j++;
    } else if (j >= m || (i < n && lcs[i + 1][j] >= lcs[i][j + 1])) {
      removed.push({ text: a[i], no: i + 1 });
      i++;
    } else {
      added.push({ text: b[j], no: j + 1 });
      j++;
    }
  }
  flush();
  return rows;
}

// hasChanges reports whether any row differs between the two sides.
export function hasChanges(rows) {
  return rows.some(row => row.kind !== "same" && row.kind !== "fold");
}

// compact keeps `context` unchanged lines around every change and folds the rest of each unchanged run into one
// {kind: "fold", count, rows} row; rows holds the hidden lines so the view can expand the fold. A run of a single
// hidden line is shown as it is, since a fold row would take the same space.
export function compact(rows, context = 3) {
  const out = [];
  let start = 0;
  while (start < rows.length) {
    if (rows[start].kind !== "same") {
      out.push(rows[start]);
      start++;
      continue;
    }
    let end = start;
    while (end < rows.length && rows[end].kind === "same") end++;
    const run = rows.slice(start, end);
    const keepHead = start === 0 ? 0 : context;
    const keepTail = end === rows.length ? 0 : context;
    if (run.length - keepHead - keepTail > 1) {
      out.push(...run.slice(0, keepHead));
      const hidden = run.slice(keepHead, run.length - keepTail);
      out.push({ kind: "fold", count: hidden.length, rows: hidden });
      out.push(...run.slice(run.length - keepTail));
    } else {
      out.push(...run);
    }
    start = end;
  }
  return out;
}

// expandFold replaces the fold row at index with the lines it hides.
export function expandFold(rows, index) {
  const fold = rows[index];
  if (!fold || fold.kind !== "fold") return rows;
  return [...rows.slice(0, index), ...fold.rows, ...rows.slice(index + 1)];
}

// The one-line restart outcome for a drift's change class.
export function restartOutcome(changeType) {
  switch (changeType) {
    case "breaking": return "Restart will recreate the cell";
    case "additive":
    case "compatible": return "Restart will update in place";
    case "none": return "Restart will record the new render inputs; the cell spec does not change";
    default: return "";
  }
}
