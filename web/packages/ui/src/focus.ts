import { useEffect, useRef, type RefObject } from "react";

// Keyboard focus helpers (#1567, item B).

// Focus can only return to an element that is still in the document and rendered: not removed,
// not inside a closed <details>, not display: none. The body is never a useful target.
function canReceiveFocus(element: Element | null): element is HTMLElement {
  return element instanceof HTMLElement && element !== document.body && element.isConnected && element.getClientRects().length > 0;
}

// Records the focused element now; the returned function focuses it again, or the fallback when it is gone.
// A button inside a closed <details> still has layout boxes in Chromium but refuses focus, so each
// candidate is tried in turn and kept only if it actually became the active element.
export function rememberFocus(fallback?: () => HTMLElement | null | undefined): () => void {
  const previous = document.activeElement;
  return () => {
    for (const target of [previous, fallback?.() ?? null]) {
      if (!canReceiveFocus(target)) continue;
      target.focus();
      if (document.activeElement === target) return;
    }
  };
}

// Opens the dialog as a modal for the component's lifetime and, when it unmounts (Cancel, Escape,
// backdrop click or Confirm), returns focus to the control that opened it, or to the fallback when
// that control is no longer rendered, for example a button inside a menu that has since closed.
export function useModalDialog(dialog: RefObject<HTMLDialogElement | null>, fallback?: () => HTMLElement | null | undefined) {
  const fallbackRef = useRef(fallback);
  fallbackRef.current = fallback;
  useEffect(() => {
    const element = dialog.current;
    if (!element) return;
    const restore = rememberFocus(() => fallbackRef.current?.());
    element.showModal();
    return () => { if (element.open) element.close(); restore(); };
  }, [dialog]);
}
