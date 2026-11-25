import type { InventoryResource } from "./inventory";
import { clearGroupings } from "./cell-filter";
import { configLabel, groupingKeys } from "./cells.mjs";
import { cellListStyleLabels, cellListStyles, profileGroups, settingDefinitions, themeLabels, themes, useSettings, type SwitchName } from "./settings";

function Note({ id, text }: { id: string; text?: string }) {
  return text ? <small className="settings-note" id={id}>{text}</small> : null;
}

// SettingsPage is the full-page Settings screen (#1686), in place of the dialog of #1652. General holds
// Appearance; Profile holds the UI configuration in its groups. Every change applies at once and is
// stored in this browser only.
export function SettingsPage({ section, inventory }: { section: "general" | "profile"; inventory: InventoryResource }) {
  const [settings, set] = useSettings();
  // Default Group by offers every label key Group by can offer across the whole inventory, stopped cells
  // included, and always kukeon.io/config and the current value.
  const current = settings.defaultGroupBy;
  const keys = [...new Set([...groupingKeys(inventory.data?.rows[3] ?? [], current), configLabel, ...(current ? [current] : [])])].sort();
  const switchRow = (name: SwitchName) => {
    const { label, note } = settingDefinitions[name];
    const on = settings[name];
    return <div className="settings-switch" key={name}>
      <span className="settings-label"><span id={`setting-${name}`}>{label}</span><Note id={`setting-${name}-note`} text={note} /></span>
      <button type="button" role="switch" aria-checked={on} aria-labelledby={`setting-${name}`} aria-describedby={note ? `setting-${name}-note` : undefined} onClick={() => set(name, !on)}>
        <span className="switch-track" aria-hidden="true"><span className="switch-thumb" /></span>
        <span className="switch-text" aria-hidden="true">{on ? "On" : "Off"}</span>
      </button>
    </div>;
  };
  return <div className="settings-page">
    <p className="settings-storage">Stored in this browser only.</p>
    {section === "general" ? <section className="panel settings-section" aria-labelledby="settings-general">
      <h2 id="settings-general">General</h2>
      <fieldset className="settings-choice settings-appearance">
        <legend>{settingDefinitions.theme.label}</legend>
        {themes.map(theme => <label key={theme}>
          <input type="radio" name="appearance" value={theme} checked={settings.theme === theme} onChange={() => set("theme", theme)} />
          {themeLabels[theme]}
        </label>)}
      </fieldset>
    </section> : <section className="panel settings-section" aria-labelledby="settings-profile">
      <h2 id="settings-profile">Profile</h2>
      {profileGroups.map(group => <section className="settings-group" key={group.title} aria-label={group.title}>
        <h3>{group.title}</h3>
        {group.settings.map(name => name === "cellListStyle" ? <fieldset className="settings-choice" key={name}>
          <legend>{settingDefinitions.cellListStyle.label}</legend>
          {cellListStyles.map(style => <label key={style}>
            <input type="radio" name="cell-list-style" value={style} checked={settings.cellListStyle === style} onChange={() => set("cellListStyle", style)} />
            {cellListStyleLabels[style]}
          </label>)}
        </fieldset> : name === "defaultGroupBy" ? <div className="settings-select" key={name}>
          <span className="settings-label"><label htmlFor="setting-defaultGroupBy">{settingDefinitions.defaultGroupBy.label}</label><Note id="setting-defaultGroupBy-note" text={settingDefinitions.defaultGroupBy.note} /></span>
          {/* A new default replaces each list's own choice, so it shows in both lists at once. */}
          <select id="setting-defaultGroupBy" value={current} aria-describedby="setting-defaultGroupBy-note" onChange={event => { set("defaultGroupBy", event.target.value); clearGroupings(); }}>
            <option value="">None</option>
            {keys.map(key => <option key={key} value={key}>{key}</option>)}
          </select>
        </div> : switchRow(name))}
      </section>)}
    </section>}
  </div>;
}
