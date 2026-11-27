import { useCallback, useSyncExternalStore } from "react";
import { configLabel } from "./cells.mjs";
import { defaultRefreshSeconds, parseRefreshSeconds, parseShowGetStarted, parseSwitch, type RefreshSeconds } from "./settings-values.mjs";

// Per-browser UI preferences (#1652, #1686, #1745). Each setting is one row of
// this table: a localStorage key, a label, and a default. A missing or unreadable
// key reads as the default, and a failed write keeps the value for this page
// only. Nothing is stored in kukeond.

export type Theme = "system" | "light" | "dark";
export const themes: readonly Theme[] = ["system", "light", "dark"];
export const themeLabels: Record<Theme, string> = { system: "System", light: "Light", dark: "Dark" };

// How the Workbench sidebar, rail and grid list cells: as agents (Configs)
// and their instances, or as the flat or label-grouped list (#1686).
export type CellListStyle = "agent" | "default";
export const cellListStyles: readonly CellListStyle[] = ["agent", "default"];
export const cellListStyleLabels: Record<CellListStyle, string> = { agent: "Agent", default: "Default" };

type SettingTypes = {
  theme: Theme;
  cellListStyle: CellListStyle;
  defaultGroupBy: string;
  showCellScope: boolean;
  showTerminalStatus: boolean;
  showUnreadBytes: boolean;
  showSidebarSearch: boolean;
  showSidebarGroupBy: boolean;
  showGridSearch: boolean;
  showGridGroupBy: boolean;
  autoAttach: boolean;
  refreshSeconds: RefreshSeconds;
  showGetStarted: boolean;
};
export type SettingName = keyof SettingTypes;
export type Settings = { [K in SettingName]: SettingTypes[K] };
export type SwitchName = { [K in SettingName]: SettingTypes[K] extends boolean ? K : never }[SettingName];

type Definition<T> = {
  key: string;
  label: string;
  defaultValue: T;
  // Shown next to the control: when the setting has no effect.
  note?: string;
  parse: (raw: string) => T | undefined;
  format: (value: T) => string;
};

const defaultStyleOnly = "Applies to the Default cell list style.";

const switchSetting = (key: string, label: string, defaultValue: boolean, note?: string): Definition<boolean> => ({
  key, label, defaultValue, note,
  parse: parseSwitch,
  format: value => String(value),
});

export const settingDefinitions: { [K in SettingName]: Definition<SettingTypes[K]> } = {
  theme: {
    key: "kukeon-theme", label: "Appearance", defaultValue: "system",
    parse: raw => themes.includes(raw as Theme) ? raw as Theme : undefined,
    format: value => value,
  },
  cellListStyle: {
    key: "kukeon-cell-list-style", label: "Cell list style", defaultValue: "agent",
    parse: raw => cellListStyles.includes(raw as CellListStyle) ? raw as CellListStyle : undefined,
    format: value => value,
  },
  // A label key, or the empty string for None.
  defaultGroupBy: {
    key: "kukeon-default-group-by", label: "Default Group by", defaultValue: configLabel, note: defaultStyleOnly,
    parse: raw => raw,
    format: value => value,
  },
  showCellScope: switchSetting("kukeon-sidebar-show-scope", "Show cell scope in sidebar", true),
  showTerminalStatus: switchSetting("kukeon-sidebar-show-terminal-status", "Show terminal status in sidebar", true),
  // The amount of a cell's unread terminal output beside its unread mark (#1696).
  showUnreadBytes: switchSetting("kukeon-show-unread-bytes", "Show unread bytes", false),
  showSidebarSearch: switchSetting("kukeon-sidebar-show-search", "Show search in sidebar", false),
  showSidebarGroupBy: switchSetting("kukeon-sidebar-show-group-by", "Show Group by in sidebar", false, defaultStyleOnly),
  showGridSearch: switchSetting("kukeon-grid-show-search", "Show search in grid", true),
  showGridGroupBy: switchSetting("kukeon-grid-show-group-by", "Show Group by in grid", true, defaultStyleOnly),
  // The cell terminal attaches by itself whenever the cell is started and nothing is attached (#1745).
  autoAttach: switchSetting("kukeon-terminal-auto-attach", "Auto-attach terminals", true),
  // How often the inventory is read while the tab is visible.
  refreshSeconds: {
    key: "kukeon-refresh-seconds", label: "Refresh interval", defaultValue: defaultRefreshSeconds,
    parse: parseRefreshSeconds,
    format: value => String(value),
  },
  // The Workbench's Get started card (#1694): On until dismissed. The key once held a card mode, which
  // parseShowGetStarted still reads.
  showGetStarted: {
    key: "kukeon-get-started", label: "Show Get started", defaultValue: true,
    parse: parseShowGetStarted,
    format: value => String(value),
  },
};

// The Settings page: its groups and their settings, in order.
export const profileGroups: readonly { title: string; settings: readonly SettingName[] }[] = [
  { title: "Appearance", settings: ["theme"] },
  { title: "Terminal", settings: ["autoAttach"] },
  { title: "Refresh", settings: ["refreshSeconds"] },
  { title: "Workbench", settings: ["showGetStarted"] },
  { title: "Cell list", settings: ["cellListStyle", "defaultGroupBy"] },
  { title: "Sidebar", settings: ["showCellScope", "showTerminalStatus", "showUnreadBytes", "showSidebarSearch", "showSidebarGroupBy"] },
  { title: "Workbench grid", settings: ["showGridSearch", "showGridGroupBy"] },
];

function read<K extends SettingName>(name: K): SettingTypes[K] {
  const definition = settingDefinitions[name];
  try {
    const raw = localStorage.getItem(definition.key);
    return raw === null ? definition.defaultValue : definition.parse(raw) ?? definition.defaultValue;
  } catch { return definition.defaultValue; }
}

// Derived from the table, so a new row cannot be forgotten.
function readAll(): Settings {
  return Object.fromEntries((Object.keys(settingDefinitions) as SettingName[]).map(name => [name, read(name)])) as Settings;
}

let current: Settings = readAll();
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function setSetting<K extends SettingName>(name: K, value: SettingTypes[K]) {
  const definition = settingDefinitions[name];
  try { localStorage.setItem(definition.key, definition.format(value)); } catch { /* Preferences are optional. */ }
  current = { ...current, [name]: value };
  for (const listener of listeners) listener();
}

// useSettings returns every setting and a setter; all components share one store.
export function useSettings(): [Settings, <K extends SettingName>(name: K, value: SettingTypes[K]) => void] {
  const settings = useSyncExternalStore(subscribe, () => current);
  const set = useCallback(<K extends SettingName>(name: K, value: SettingTypes[K]) => setSetting(name, value), []);
  return [settings, set];
}
