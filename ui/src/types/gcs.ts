// Google Cloud Storage. See docs/specs/gcs-scans.md.

/** One of an account's Cloud projects. */
export type GcsProject = {
  projectId: string;
  displayName: string;
};

/** A bucket and the settings that decide what it costs. */
export type GcsBucket = {
  name: string;
  location: string;
  // The default class for new objects.
  storageClass: string;
  versioning: boolean;
  // Missing when soft delete is off.
  softDeleteDays?: number;
  // Listing it would bill this app's project, so scans skip it.
  requesterPays: boolean;
};

/** A Cloud Storage scan request. */
export type GStorageScan = {
  ClientKey: string;
  ProjectId: string;
  // "" for every bucket of the project.
  Bucket: string;
  // Only with a bucket.
  Prefix: string;
  Versions: boolean;
  SoftDeleted: boolean;
};

/** Objects and bytes by state, and live bytes by storage class. */
export type GcsTotals = {
  live_objects: number;
  live_bytes: number;
  noncurrent_objects: number;
  noncurrent_bytes: number;
  soft_deleted_objects: number;
  soft_deleted_bytes: number;
  bytes_by_class: Record<string, number> | null;
};

/** What a scan did with one bucket. */
export type GcsScanBucket = GcsTotals & {
  bucket: string;
  project_id: string;
  prefix: string;
  status: "completed" | "failed" | "skipped";
  error: string;
};
