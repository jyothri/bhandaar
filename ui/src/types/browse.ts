// What Browse shows. See docs/specs/browse.md, "Browse API".

import { MessageRow } from "./results";

/** What's recorded of one service of a Google account. */
export type ServiceTotals = {
  // Whether the account has granted access to the service.
  granted: boolean;
  // Missing until something is recorded.
  files?: number;
  bytes?: number;
  updated_at?: string;
  // The folder totals are being rebuilt (Drive).
  updating?: boolean;
};

/** A Google account or an agent drive. */
export type BrowseSource = {
  kind: "google" | "agent";
  // A Google account's client key, or an agent drive's ID.
  key: string;
  name: string;
  // A Google account's services.
  services?: { drive: ServiceTotals; gmail: ServiceTotals };
  // An agent drive's totals, last sync, and linked physical drive.
  files?: number;
  bytes?: number;
  updated_at?: string;
  physical_drive?: number;
  updating?: boolean;
};

/** The folder ID the backend gives Drive's "Shared with me" root. */
export const sharedWithMe = "shared-with-me";

export type PathPart = { id: string; name: string };

export type BrowseFolder = {
  // A Drive folder ID, or an agent folder's path on the drive.
  id: string;
  name: string;
  // Everything under it, at any depth.
  files: number;
  bytes: number;
};

export type BrowseFile = {
  id: string;
  name: string;
  // Null for an agent file not scanned yet, or unreadable (see error).
  size: number | null;
  modified: string | null;
  mime_type?: string;
  // Why an agent couldn't read it.
  error?: string;
};

/** A page of a folder: subfolders, then files, each largest first. */
export type FolderPage = {
  // From the source's root down to the folder; empty at the root.
  path: PathPart[];
  folders: BrowseFolder[];
  files: BrowseFile[];
  // Subfolders and files in all, across pages.
  entries: number;
  page: number;
  page_size: number;
  // The source's folder totals are being rebuilt.
  updating: boolean;
};

export type AgentScanRun = {
  started_at: string;
  finished_at: string | null;
  files_seen: number | null;
  interrupted: boolean | null;
};

export type AgentDriveStatus = {
  last_scan: AgentScanRun | null;
  last_synced_at: string | null;
  physical_drive: number | null;
  updating: boolean;
};

export type AccountMessage = MessageRow & {
  labels: string;
  // The scan that found it.
  scan_id: number;
};

export type AccountMessagePage = {
  messages: AccountMessage[];
  total: number;
  page: number;
  page_size: number;
};
