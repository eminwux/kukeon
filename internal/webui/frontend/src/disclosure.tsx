import { useState, type ReactNode } from "react";

// Collapsible panels (#1568). A panel's heading row is the <summary>, so it opens with a click, Enter or
// Space, and it shows a one-line digest of its content while collapsed.
const storageKey = "kukeon-disclosures";

function storedStates(): Record<string, unknown> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(storageKey) ?? "{}");
    return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
  } catch { return {}; }
}

// useDisclosure remembers a section's open state under its id; sections start collapsed unless stored open.
export function useDisclosure(id: string, defaultOpen = false): [boolean, (open: boolean) => void] {
  const [open, setOpen] = useState(() => { const stored = storedStates()[id]; return typeof stored === "boolean" ? stored : defaultOpen; });
  return [open, next => {
    setOpen(next);
    try { localStorage.setItem(storageKey, JSON.stringify({ ...storedStates(), [id]: next })); } catch { /* Preferences are optional. */ }
  }];
}

// Disclosure is the controlled panel: the caller owns the open state (a form opens the sections that hold
// invalid fields). StoredDisclosure below is the self-contained variant that remembers the operator's choice.
export function Disclosure({ title, summary, badge, className = "", label, open, onToggle, children }: {
  title: string;
  summary?: ReactNode;
  badge?: ReactNode;
  className?: string;
  label?: string;
  open: boolean;
  onToggle: (open: boolean) => void;
  children: ReactNode;
}) {
  return <section className={`panel disclosure ${className}`.trim()} aria-label={label}>
    <details open={open} onToggle={event => { if (event.currentTarget.open !== open) onToggle(event.currentTarget.open); }}>
      <summary className="panel-heading"><h2>{title}</h2><span className="disclosure-summary">{summary}</span>{badge}</summary>
      {children}
    </details>
  </section>;
}

export function StoredDisclosure({ id, ...props }: Omit<Parameters<typeof Disclosure>[0], "open" | "onToggle"> & { id: string }) {
  const [open, setOpen] = useDisclosure(id);
  return <Disclosure {...props} open={open} onToggle={setOpen} />;
}

// Cell and container pages tuck their runtime internals (network, cgroup) behind disclosures; the realm,
// space and stack pages keep the plain panels.
export const disclosedKinds = ["cell", "container"];
