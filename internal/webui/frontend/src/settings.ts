import { useCallback, useSyncExternalStore } from "react";

// Per-browser UI preferences (#1652). Each setting is one row of this table:
// a localStorage key, a label, and a default. A missing or unreadable key
// reads as the default, and a failed write keeps the value for this page
// only. Nothing is stored in kukeond.

export type Theme = "system" | "light" | "dark";
export const themes: readonly Theme[] = ["system", "light", "dark"];
export const themeLabels: Record<Theme, string> = { system: "System", light: "Light", dark: "Dark" };

type SettingTypes = {
  theme: Theme;
  showCellScope: boolean;
  showTerminalStatus: boolean;
};
export type SettingName = keyof SettingTypes;
export type Settings = { [K in SettingName]: SettingTypes[K] };

type Definition<T> = {
  key: string;
  label: string;
  defaultValue: T;
  parse: (raw: string) => T | undefined;
  format: (value: T) => string;
};

const switchSetting = (key: string, label: string): Definition<boolean> => ({
  key, label, defaultValue: true,
  parse: raw => raw === "true" ? true : raw === "false" ? false : undefined,
  format: value => String(value),
});

export const settingDefinitions: { [K in SettingName]: Definition<SettingTypes[K]> } = {
  theme: {
    key: "kukeon-theme", label: "Appearance", defaultValue: "system",
    parse: raw => themes.includes(raw as Theme) ? raw as Theme : undefined,
    format: value => value,
  },
  showCellScope: switchSetting("kukeon-sidebar-show-scope", "Show cell scope in sidebar"),
  showTerminalStatus: switchSetting("kukeon-sidebar-show-terminal-status", "Show terminal status in sidebar"),
};

// The boolean settings the Settings dialog renders as switches, in order.
export const switchSettings = ["showCellScope", "showTerminalStatus"] as const;

function read<K extends SettingName>(name: K): SettingTypes[K] {
  const definition = settingDefinitions[name];
  try {
    const raw = localStorage.getItem(definition.key);
    return raw === null ? definition.defaultValue : definition.parse(raw) ?? definition.defaultValue;
  } catch { return definition.defaultValue; }
}

function readAll(): Settings {
  return { theme: read("theme"), showCellScope: read("showCellScope"), showTerminalStatus: read("showTerminalStatus") };
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
