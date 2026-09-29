// Google Cloud Storage names, checked as the backend checks them
// (be/collect/gcs.go). See docs/specs/gcs-scans.md.

/** A project ID, optionally in a domain ("example.com:my-project"). */
export function validProjectId(id: string): boolean {
  return /^([a-z0-9][a-z0-9.-]{0,60}:)?[a-z][a-z0-9-]{4,28}[a-z0-9]$/.test(id);
}

/** The longest prefix a scan takes: an object name's longest, in bytes. */
export const MAX_PREFIX_BYTES = 1024;
