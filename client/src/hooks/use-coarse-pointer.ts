import { useSyncExternalStore } from "react";

// useCoarsePointer reports whether the primary input is a touch screen.
// Used to suppress composer auto-focus on phones/tablets, where focusing an
// input pops the software keyboard over half the viewport.
const query = "(pointer: coarse)";

function subscribe(onChange: () => void): () => void {
  const mql = window.matchMedia(query);
  mql.addEventListener("change", onChange);
  return () => mql.removeEventListener("change", onChange);
}

function getSnapshot(): boolean {
  return window.matchMedia(query).matches;
}

export function useCoarsePointer(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot);
}
