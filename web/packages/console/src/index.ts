// @kukeon/console: the kukeon web console. A host renders <KukeonConsole /> (or calls mountConsole) and
// imports @kukeon/console/style.css once; every seam defaults to what kuke serve uses.
export { KukeonConsole, mountConsole, type ConsoleOptions } from "./main";
export type { Transport } from "@kukeon/api-client";
export type { Router } from "./router";
export type { AuthAdapter, Session, SessionCheck, SignInProps } from "./auth";
