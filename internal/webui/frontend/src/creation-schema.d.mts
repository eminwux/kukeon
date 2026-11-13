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
};
export const containerFields: Field[];
export const cellAdvanced: Field[];
