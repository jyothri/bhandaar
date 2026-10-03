// The Duplicates page: identical files and folders across a user's sources,
// and likely copies of their Google Photos. See docs/specs/duplicates.md.

export type DupKind = "file" | "folder" | "photo";

/**
 * A source: "google:<client key>:drive", "google:<client key>:gcs:<bucket>",
 * "google:<client key>:photos" or "agent:<drive ID>".
 */
export type DupSource = string;

export type DupSourceTotal = {
  source: DupSource;
  label: string;
  // Its copies in groups of identical files, not all on one physical drive.
  bytes: number;
  files: number;
};

/** A source's folders that couldn't be compared, and why. */
export type Uncomparable = {
  source: DupSource;
  label: string;
  folders: number;
  reason: string;
};

export type DupSummary = {
  // Of identical files; folders' bytes are their files'.
  reclaimable: number;
  groups: Record<DupKind, number>;
  by_source: DupSourceTotal[];
  uncomparable: Uncomparable[];
  // null until the first build.
  built_at: string | null;
  // A build is running (or the first is about to).
  updating: boolean;
  took_ms: number;
  // Why the last build failed, and when: the index shown, if any, is older.
  error?: string;
  failed_at?: string;
};

/** One copy. */
export type DupMember = {
  source: DupSource;
  label: string;
  // Drive's file or folder ID, a Photos media item ID, else the path.
  item: string;
  path: string;
  // What Browse opens: Drive's folder ID, "<bucket>/<prefix>", or an agent
  // drive's folder path; "" for Photos.
  folder: string;
  size: number;
  // A folder's files.
  files: number;
  modified: string | null;
  physical_drive: number | null;
  // Shared with the user, not theirs: not counted as reclaimable.
  shared: boolean;
};

export type DupGroup = {
  kind: DupKind;
  // With kind, names the group across rebuilds of the index.
  key: string;
  name: string;
  // One copy's.
  size: number;
  files: number;
  copies: number;
  reclaimable: number;
  same_physical: boolean;
  sources: DupSource[];
  // For photos: what matched, e.g. "name and capture time, dimensions".
  match: string;
  // Up to 10; the rest come from getDupMembers.
  members: DupMember[];
};

export type DupGroupsPage = {
  groups: DupGroup[];
  // The sources' names.
  labels: Record<DupSource, string>;
  total: number;
  page: number;
  page_size: number;
};

export type DupMembersPage = {
  members: DupMember[];
  total: number;
  page: number;
  page_size: number;
};

export type DupFilter = {
  kind: DupKind;
  source?: DupSource;
  across?: boolean;
  min_size?: number;
  hide_same_physical?: boolean;
  page?: number;
};
