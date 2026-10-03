// The Manage data page: linked accounts, uploaded drives, and deleting them.
// See docs/specs/data-deletion.md.

import { ServiceTotals } from "./browse";

/** A deletion running in the background, and how it went. */
export type DeletionJob = {
  id: number;
  // A drive from one box, a whole account, or one service of an account.
  kind: "agent_drive" | "account" | RecordedService;
  target: string;
  label: string;
  status: "running" | "done" | "failed";
  // What was deleted, e.g. { files: 888902 }.
  counts: Record<string, number>;
  // For an account: "revoked", "already revoked", or "failed: …".
  revoke?: string;
  error?: string;
  started_at: string;
  finished_at: string | null;
};

export type RecordedService = "gmail" | "drive" | "gcs" | "photos";

export type ManageAccount = {
  client_key: string;
  // The name to type to disconnect it.
  label: string;
  services: string[];
  recorded: Record<RecordedService, Omit<ServiceTotals, "granted">>;
  // Scans per service, and in all.
  service_scans: Partial<Record<RecordedService, number>>;
  scans: number;
  // A running scan, which blocks deleting the account's data.
  running_scan?: number;
  job: DeletionJob | null;
};

export type ManageDrive = {
  id: number;
  drive_id: string;
  hostname: string;
  files: number;
  bytes: number;
  last_synced_at: string | null;
  physical_drive?: number;
  // The other boxes this physical drive was uploaded from.
  other_copies: string[];
  job: DeletionJob | null;
};

export type ManageData = {
  accounts: ManageAccount[];
  drives: ManageDrive[];
};
