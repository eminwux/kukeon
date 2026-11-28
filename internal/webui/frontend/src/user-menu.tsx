import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { homeLink, settingsLink } from "./routes";

// UserMenu is the top-bar avatar button (#1652): its menu names the signed-in
// user, reopens the Workbench's Get started card (#1694), links to the
// Settings page (#1686) and signs out. Escape closes the menu and returns
// focus to the button.
export function UserMenu({ user, onGetStarted, onSignOut }: { user: string; onGetStarted: () => void; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const close = (refocus: boolean) => { setOpen(false); if (refocus) button.current?.focus(); };
  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector<HTMLElement>("[role=menuitem]")?.focus();
    const outside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!menu.current?.contains(target) && !button.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  function menuKey(event: ReactKeyboardEvent) {
    const items = [...(menu.current?.querySelectorAll<HTMLElement>("[role=menuitem]") ?? [])];
    const index = items.indexOf(document.activeElement as HTMLElement);
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(true); }
    else if (event.key === "Tab") setOpen(false);
    else if (event.key === "ArrowDown") { event.preventDefault(); items[(index + 1) % items.length]?.focus(); }
    else if (event.key === "ArrowUp") { event.preventDefault(); items[(index - 1 + items.length) % items.length]?.focus(); }
    // A menu item activates on Space; a link does so natively only on Enter.
    else if (event.key === " " && event.target instanceof HTMLAnchorElement) { event.preventDefault(); event.target.click(); }
  }
  const initial = user.slice(0, 1).toUpperCase() || "?";
  return <div className="user-menu">
    <button ref={button} className="icon-button user-button" type="button" aria-label={`User menu: ${user}`} title={user}
      aria-haspopup="menu" aria-expanded={open} aria-controls="user-menu-list" onClick={() => setOpen(value => !value)}>
      <span className="user-avatar" aria-hidden="true">{initial}</span>
    </button>
    {open && <div className="user-menu-popup" id="user-menu-list" role="menu" aria-label="User" ref={menu} onKeyDown={menuKey}>
      <p className="user-menu-identity">Signed in as <strong>{user}</strong></p>
      <a role="menuitem" href={settingsLink()} onClick={() => setOpen(false)}>Settings</a>
      <a role="menuitem" href={homeLink()} onClick={() => { setOpen(false); onGetStarted(); }}>Get started</a>
      <button type="button" role="menuitem" onClick={() => { setOpen(false); onSignOut(); }}>Sign out</button>
    </div>}
  </div>;
}
