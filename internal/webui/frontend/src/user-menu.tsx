import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useModalDialog } from "./focus";
import { settingDefinitions, switchSettings, themeLabels, themes, useSettings } from "./settings";

// UserMenu is the top-bar avatar button (#1652): its menu names the signed-in
// user and opens Settings or signs out. Escape closes the menu and returns
// focus to the button; the Settings dialog does the same when it closes.
export function UserMenu({ user, onSignOut }: { user: string; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
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
  }
  const initial = user.slice(0, 1).toUpperCase() || "?";
  return <div className="user-menu">
    <button ref={button} className="icon-button user-button" type="button" aria-label={`User menu: ${user}`} title={user}
      aria-haspopup="menu" aria-expanded={open} aria-controls="user-menu-list" onClick={() => setOpen(value => !value)}>
      <span className="user-avatar" aria-hidden="true">{initial}</span>
    </button>
    {open && <div className="user-menu-popup" id="user-menu-list" role="menu" aria-label="User" ref={menu} onKeyDown={menuKey}>
      <p className="user-menu-identity">Signed in as <strong>{user}</strong></p>
      <button type="button" role="menuitem" onClick={() => { setOpen(false); setSettingsOpen(true); }}>Settings</button>
      <button type="button" role="menuitem" onClick={() => { setOpen(false); onSignOut(); }}>Sign out</button>
    </div>}
    {settingsOpen && <SettingsDialog onClose={() => setSettingsOpen(false)} returnFocus={() => button.current} />}
  </div>;
}

// SettingsDialog edits the per-browser preferences. Changes apply at once and
// persist in this browser only.
function SettingsDialog({ onClose, returnFocus }: { onClose: () => void; returnFocus: () => HTMLElement | null }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useModalDialog(dialog, returnFocus);
  const [settings, set] = useSettings();
  return <dialog className="cell-action-dialog settings-dialog" ref={dialog} aria-labelledby="settings-title"
    onCancel={event => { event.preventDefault(); onClose(); }}
    onClick={event => { if (event.target === event.currentTarget) onClose(); }}>
    <form method="dialog" onSubmit={event => { event.preventDefault(); onClose(); }}>
      <h2 id="settings-title">Settings</h2>
      <p>Stored in this browser only.</p>
      <fieldset className="settings-appearance">
        <legend>{settingDefinitions.theme.label}</legend>
        {themes.map(theme => <label key={theme}>
          <input type="radio" name="appearance" value={theme} checked={settings.theme === theme} onChange={() => set("theme", theme)} />
          {themeLabels[theme]}
        </label>)}
      </fieldset>
      {switchSettings.map(name => {
        const label = settingDefinitions[name].label;
        const on = settings[name];
        return <div className="settings-switch" key={name}>
          <span id={`setting-${name}`}>{label}</span>
          <button type="button" role="switch" aria-checked={on} aria-labelledby={`setting-${name}`} onClick={() => set(name, !on)}>
            <span className="switch-track" aria-hidden="true"><span className="switch-thumb" /></span>
            <span className="switch-text" aria-hidden="true">{on ? "On" : "Off"}</span>
          </button>
        </div>;
      })}
      <div className="cell-action-dialog-buttons">
        <button type="submit">Done</button>
      </div>
    </form>
  </dialog>;
}
