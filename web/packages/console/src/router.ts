// The router seam (#1752): how the console reads, links to and navigates between its routes. A route
// path is what follows the hash today, e.g. "/resource/dev/shared?document=1". The default router is
// the historical hash routing; a host that mounts the console elsewhere supplies its own.

export type Router = {
  // The current route path.
  current: () => string;
  // The href of a link that opens route path.
  href: (path: string) => string;
  // Opens a link that href returned.
  navigate: (href: string) => void;
  // Calls listener after every route change; returns the unsubscribe.
  subscribe: (listener: () => void) => () => void;
};

export const hashRouter: Router = {
  current: () => window.location.hash.slice(1),
  href: path => "#" + path,
  navigate: href => { window.location.hash = href; },
  subscribe: listener => {
    window.addEventListener("hashchange", listener);
    return () => window.removeEventListener("hashchange", listener);
  },
};

let current: Router = hashRouter;

// configureRouter replaces the router; mountConsole calls it before the first render.
export function configureRouter(router: Router = hashRouter) {
  current = router;
}

export function currentRoutePath(): string {
  return current.current();
}

export function routeHref(path: string): string {
  return current.href(path);
}

export function navigate(href: string) {
  current.navigate(href);
}

export function onRouteChange(listener: () => void): () => void {
  return current.subscribe(listener);
}
