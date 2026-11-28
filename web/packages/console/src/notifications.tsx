import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { failureTitle, type Resource } from "./request";
import type { Inventory } from "./inventory";
import { Icon } from "@kukeon/ui";

// Toasts carry transient results of user actions. They sit in a fixed overlay so a message appearing or
// going away never reflows the page, which on cellTerminal would resize the attached PTY.
export type ToastTone = "pending" | "success" | "warning" | "error";
export type ToastInput = { key: string; tone: ToastTone; message: string; title?: string; link?: { href: string; label: string } };
type Toast = ToastInput & { revision: number };
type Notifier = { notify: (toast: ToastInput) => void; dismiss: (key: string) => void };

const autoDismissMs = 5000;
const maxVisible = 3;
const NotificationContext = createContext<Notifier>({ notify: () => {}, dismiss: () => {} });

export function useNotify(): Notifier {
  return useContext(NotificationContext);
}

function ToastItem({ toast, onDismiss }: { toast: Toast; onDismiss: (key: string) => void }) {
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const remaining = useRef(autoDismissMs);
  // Each state change restarts the countdown; hover and focus pause it without resetting. Hover counts
  // only once the pointer moves over the toast: one that appears under a resting pointer still dismisses.
  useEffect(() => { remaining.current = autoDismissMs; }, [toast.revision]);
  useEffect(() => {
    if (toast.tone !== "success" || hovered || focused) return;
    const started = Date.now();
    const timer = window.setTimeout(() => onDismiss(toast.key), remaining.current);
    return () => { window.clearTimeout(timer); remaining.current = Math.max(0, remaining.current - (Date.now() - started)); };
  }, [toast.key, toast.tone, toast.revision, hovered, focused, onDismiss]);
  return <div
    className={`toast ${toast.tone}`}
    data-toast={toast.key}
    onPointerMove={() => setHovered(true)}
    onPointerLeave={() => setHovered(false)}
    onFocus={() => setFocused(true)}
    onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false); }}
    onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); onDismiss(toast.key); } }}
  >
    <span className="toast-icon" aria-hidden="true" />
    <div className="toast-body">
      {toast.title && <strong className="toast-title">{toast.title}</strong>}
      <p>{toast.message}</p>
      {toast.link && <a className="text-link" href={toast.link.href}>{toast.link.label}</a>}
    </div>
    <button type="button" className="toast-dismiss" aria-label="Dismiss notification" onClick={() => onDismiss(toast.key)}>×</button>
  </div>;
}

export function NotificationProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [expanded, setExpanded] = useState(false);
  // One persistent pair of live regions announces every toast; the toasts themselves declare no role.
  const [announcement, setAnnouncement] = useState({ polite: "", assertive: "", count: 0 });
  const stack = useRef<HTMLElement>(null);
  const notify = useCallback((input: ToastInput) => {
    // A keyed operation updates its toast in place; a new key goes on top.
    setToasts(old => {
      const existing = old.find(item => item.key === input.key);
      if (!existing) return [{ ...input, revision: 0 }, ...old];
      return old.map(item => item.key === input.key ? { ...input, revision: item.revision + 1 } : item);
    });
    const text = input.title ? `${input.title}: ${input.message}` : input.message;
    // Alternate a zero-width suffix so a repeated message still changes the region and is announced again.
    setAnnouncement(old => {
      const count = old.count + 1;
      const value = count % 2 ? text : `${text}​`;
      return input.tone === "error" ? { polite: "", assertive: value, count } : { polite: value, assertive: "", count };
    });
  }, []);
  const dismiss = useCallback((key: string) => setToasts(old => old.filter(item => item.key !== key)), []);
  const value = useMemo(() => ({ notify, dismiss }), [notify, dismiss]);

  // Anchor the stack under the toolbar, or at the top of the viewport once the toolbar scrolls away.
  const active = toasts.length > 0;
  useLayoutEffect(() => {
    const element = stack.current;
    const toolbar = document.querySelector<HTMLElement>(".toolbar");
    if (!active || !element) return;
    const place = () => element.style.setProperty("--toast-top", `${Math.max(0, Math.ceil(toolbar?.getBoundingClientRect().bottom ?? 0)) + 8}px`);
    place();
    const observer = new ResizeObserver(place);
    if (toolbar) observer.observe(toolbar);
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => { observer.disconnect(); window.removeEventListener("resize", place); window.removeEventListener("scroll", place, true); };
  }, [active]);
  useEffect(() => { if (toasts.length <= maxVisible) setExpanded(false); }, [toasts.length]);

  const visible = expanded ? toasts : toasts.slice(0, maxVisible);
  const hidden = toasts.length - visible.length;
  return <NotificationContext.Provider value={value}>
    {children}
    <section className="toast-stack" ref={stack} aria-label="Notifications" hidden={!active}>
      {visible.map(toast => <ToastItem key={toast.key} toast={toast} onDismiss={dismiss} />)}
      {hidden > 0 && <button type="button" className="toast-more" onClick={() => setExpanded(true)}>{hidden} older {hidden === 1 ? "notification" : "notifications"}</button>}
      {expanded && toasts.length > maxVisible && <button type="button" className="toast-more" onClick={() => setExpanded(false)}>Show fewer</button>}
    </section>
    <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">{announcement.polite}</div>
    <div className="sr-only" role="alert" aria-live="assertive" aria-atomic="true">{announcement.assertive}</div>
  </NotificationContext.Provider>;
}

// InventoryIndicator replaces the top-of-content inventory banners with a toolbar warning whose
// popover lists the failed scopes and the last successful refresh.
export function InventoryIndicator({ inventory }: { inventory: Resource<Inventory> }) {
  const popover = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const error = inventory.error;
  const state = error ? inventory.data ? "stale" : "unavailable" : inventory.data?.failures.length ? "incomplete" : "";
  if (!state) return null;
  const failures = error
    ? error.code === "partial" ? error.message.split("; ") : [`${failureTitle(error)}: ${error.message}`]
    : inventory.data?.failures ?? [];
  const summary = state === "stale" ? "The last refresh failed. Counts and lists show the previous inventory and may be out of date."
    : state === "unavailable" ? "The inventory could not be retrieved."
      : "Some scopes could not be loaded. Counts and lists exclude unavailable resources.";
  const label = `Inventory ${state}`;
  const position = () => {
    const rect = button.current?.getBoundingClientRect();
    if (!rect || !popover.current) return;
    popover.current.style.setProperty("--popover-top", `${Math.ceil(rect.bottom) + 6}px`);
    const width = Math.min(360, window.innerWidth - 32);
    popover.current.style.setProperty("--popover-left", `${Math.max(16, Math.min(Math.floor(rect.left), window.innerWidth - 16 - width))}px`);
  };
  return <>
    <button ref={button} type="button" className={`icon-button inventory-indicator ${state}`} popoverTarget="inventory-status" aria-label={`${label}: show details`} data-tooltip={label} onClick={position}><Icon name="warning" /></button>
    <div ref={popover} id="inventory-status" className="inventory-popover" popover="auto" role="dialog" aria-label="Inventory status">
      <h2>{label[0].toUpperCase()}{label.slice(1)}</h2>
      <p>{summary}</p>
      {failures.length > 0 && <ul>{failures.map(message => <li key={message}>{message}</li>)}</ul>}
      <p className="description">{inventory.data?.updatedAt ? <>Last successful refresh: <time dateTime={inventory.data.updatedAt}>{new Date(inventory.data.updatedAt).toLocaleString()}</time></> : "No successful refresh yet."}</p>
    </div>
  </>;
}
