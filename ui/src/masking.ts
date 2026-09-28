import { useSyncExternalStore } from "react";

// Masking of Gmail senders and subjects, against casual onlookers. One
// setting for every page, kept in memory only: a reload starts masked.

let unmasked = false;
// Counts the times masking was switched back on, so cells revealed one at
// a time before then are masked again.
let maskings = 0;
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function setUnmasked(value: boolean) {
  if (value === unmasked) {
    return;
  }
  unmasked = value;
  if (!value) {
    maskings++;
  }
  listeners.forEach((l) => l());
}

export const useUnmasked = () =>
  useSyncExternalStore(subscribe, () => unmasked);
export const useMaskings = () =>
  useSyncExternalStore(subscribe, () => maskings);

/** Masks tests' state again; the setting outlives a test's render. */
export function resetMasking() {
  setUnmasked(false);
}
