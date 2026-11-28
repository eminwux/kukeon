export type Field = {
  key: string;
  label: string;
  kind: "text" | "number" | "boolean" | "enum" | "strings" | "env" | "map" | "object" | "objects";
  required?: boolean;
  advanced?: boolean;
  options?: string[];
  fields?: Field[];
  itemLabel?: string;
  help?: string;
  // sourcePicker puts a "From source" select above each item (#1644): a stored Source fills the item.
  sourcePicker?: boolean;
};
export const containerFields: Field[];
export const cellAdvanced: Field[];
