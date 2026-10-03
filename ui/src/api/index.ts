import { config } from "../config";
import { Account, ScannedAccount } from "../types/accounts";
import {
  AccountMessagePage,
  AgentDriveStatus,
  BrowseSource,
  FolderPage,
} from "../types/browse";
import {
  AccountPhotoPage,
  PhotosPick,
  PickedItemPage,
} from "../types/photos";
import {
  DupFilter,
  DupGroupsPage,
  DupKind,
  DupMembersPage,
  DupSummary,
} from "../types/duplicates";
import { GcsBucket, GcsProject, GcsScanBucket } from "../types/gcs";
import {
  DeletionJob,
  RecordedService,
  ManageData,
} from "../types/manageData";
import { MessagePage, ScanDataPage, ScanSummary } from "../types/results";
import { RequestScanResponse, ScanMetadata, ScanRequest } from "../types/scans";

export const backend_url = config.backendUrl;

/** Thrown for a 401: there's no session, or it has ended. */
export class UnauthenticatedError extends Error {}

/**
 * Fetches a backend path, sending the session cookie (the backend is on
 * another origin in dev). Throws UnauthenticatedError for a 401, and an Error
 * with the backend's message for any other non-2xx response.
 */
async function fetchBackend(path: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(backend_url + path, {
    credentials: "include",
    ...init,
  });
  if (response.status === 401) {
    throw new UnauthenticatedError(await errorMessage(response));
  }
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
  return response;
}

/** Like fetchBackend, and parses the JSON response. */
async function fetchJson<T>(path: string, init?: RequestInit): Promise<T> {
  return (await fetchBackend(path, init)).json();
}

export type User = { username: string };

/** Logs in, setting the session cookie. Wrong credentials throw an Error. */
export async function login(username: string, password: string): Promise<User> {
  const response = await fetch(backend_url + "/api/auth/login", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
  return response.json();
}

/** Ends the session. */
export const logout = async (): Promise<void> => {
  await fetchBackend("/api/auth/logout", { method: "POST" });
};

/** The logged-in user, or null without a session. */
export async function getMe(): Promise<User | null> {
  try {
    return await fetchJson<User>("/api/auth/me");
  } catch (e) {
    if (e instanceof UnauthenticatedError) {
      return null;
    }
    throw e;
  }
}

/**
 * The backend replies with plain text (http.Error) or JSON of the form
 * { error: { message } }. Anything else, e.g. a proxy's HTML error page,
 * falls back to the HTTP status.
 */
async function errorMessage(response: Response): Promise<string> {
  const status = `${response.status} ${response.statusText}`.trim();
  const contentType = response.headers.get("Content-Type") ?? "";
  const text = (await response.text()).trim();
  if (contentType.includes("application/json")) {
    try {
      const { error } = JSON.parse(text);
      const message = typeof error === "string" ? error : error?.message;
      if (message) {
        return message;
      }
    } catch {
      // Fall through to the status.
    }
  } else if (contentType.includes("text/plain") && text) {
    return text;
  }
  return status;
}

/**
 * Function to submit scan request.
 */
export const requestScan = (
  scanData: ScanMetadata
): Promise<RequestScanResponse> =>
  fetchJson("/api/scans", {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify(scanData),
  });

/** Starts picking in Google Photos for a linked account. */
export const startPhotosPick = (clientKey: string): Promise<PhotosPick> =>
  fetchJson("/api/photos/sessions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ clientKey }),
  });

/** The user's pick that is waiting or scanning, if any. */
export const getActivePhotosPick = (): Promise<PhotosPick | null> =>
  fetchJson("/api/photos/sessions");

/** A pick's state, and its scan once picked. */
export const getPhotosPick = (sessionKey: string): Promise<PhotosPick> =>
  fetchJson(`/api/photos/sessions/${encodeURIComponent(sessionKey)}`);

/** Cancels a pick the user hasn't picked in yet. */
export const cancelPhotosPick = async (sessionKey: string): Promise<void> => {
  await fetchBackend(`/api/photos/sessions/${encodeURIComponent(sessionKey)}`, {
    method: "DELETE",
  });
};

/**
 * Function to get list of accounts.
 */
export const getAccounts = (): Promise<Account[]> =>
  fetchJson("/api/accounts");

/**
 * Function to get accounts for which requests were submitted.
 */
export const getScannedAccounts = (): Promise<ScannedAccount[]> =>
  fetchJson("/api/scans/accounts");

/**
 * The scans of one account, by its client key.
 */
export const getScanRequests = (clientKey: string): Promise<ScanRequest[]> =>
  fetchJson(`/api/scans/requests/${encodeURIComponent(clientKey)}`);

/** A scan's details and totals. */
export const getScanSummary = (scanId: number): Promise<ScanSummary> =>
  fetchJson(`/api/scans/${scanId}/summary`);

/** A page (from 1) of the files and folders a scan found. */
export const getScanData = (scanId: number, page: number): Promise<ScanDataPage> =>
  fetchJson(`/api/scans/${scanId}?page=${page}`);

/** A page (from 1) of the messages a Gmail scan found. */
export const getGmailData = (scanId: number, page: number): Promise<MessagePage> =>
  fetchJson(`/api/gmaildata/${scanId}?page=${page}`);

/** A page (from 1) of the items a Google Photos scan picked. */
export const getPickedItems = (
  scanId: number,
  page: number
): Promise<PickedItemPage> => fetchJson(`/api/photos/${scanId}?page=${page}`);

/** What the user can browse: their Google accounts and agent drives. */
export const getBrowseSources = (): Promise<BrowseSource[]> =>
  fetchJson("/api/browse/sources");

/**
 * A page (from 1) of a folder of a Google account's Drive ("" for its
 * roots), of an agent drive ("" for the drive's root), or of a Google
 * account's Cloud Storage ("" for its buckets, else "<bucket>/<prefix>").
 */
export const getBrowseChildren = (
  kind: "google" | "agent" | "gcs",
  key: string,
  folder: string,
  page: number
): Promise<FolderPage> => {
  const base =
    kind === "google"
      ? `/api/browse/google/${encodeURIComponent(key)}/drive/children`
      : kind === "gcs"
        ? `/api/browse/google/${encodeURIComponent(key)}/gcs/children`
        : `/api/browse/agent/${encodeURIComponent(key)}/children`;
  const query = new URLSearchParams({ folder, page: String(page) });
  return fetchJson(`${base}?${query}`);
};

/** An agent drive's last scan, last sync and linked physical drive. */
export const getAgentDriveStatus = (key: string): Promise<AgentDriveStatus> =>
  fetchJson(`/api/browse/agent/${encodeURIComponent(key)}/status`);

/** A page (from 1) of a Google account's messages, across its Gmail scans. */
export const getAccountMessages = (
  clientKey: string,
  sort: "size" | "date",
  page: number
): Promise<AccountMessagePage> =>
  fetchJson(
    `/api/browse/google/${encodeURIComponent(clientKey)}/gmail/messages?sort=${sort}&page=${page}`
  );

/** A page (from 1) of a Google account's picked Photos, across its scans. */
export const getAccountPhotos = (
  clientKey: string,
  sort: "size" | "date",
  page: number
): Promise<AccountPhotoPage> =>
  fetchJson(
    `/api/browse/google/${encodeURIComponent(clientKey)}/photos/items?sort=${sort}&page=${page}`
  );

/** A linked account's active Cloud projects. */
export const getGcsProjects = (clientKey: string): Promise<GcsProject[]> =>
  fetchJson(`/api/gcs/${encodeURIComponent(clientKey)}/projects`);

/** A project's buckets, with their settings. */
export const getGcsBuckets = (
  clientKey: string,
  project: string
): Promise<GcsBucket[]> =>
  fetchJson(
    `/api/gcs/${encodeURIComponent(clientKey)}/projects/${encodeURIComponent(project)}/buckets`
  );

/** What a Cloud Storage scan did with each bucket. */
export const getGcsScanBuckets = (scanId: number): Promise<GcsScanBucket[]> =>
  fetchJson(`/api/gcs/${scanId}`);

/** The user's linked accounts and uploaded drives, for Manage data. */
export const getManageData = (): Promise<ManageData> =>
  fetchJson("/api/manage-data");

/** Deletes a drive as uploaded from one box; the deletion runs as a job. */
export const deleteAgentDrive = (id: number): Promise<DeletionJob> =>
  fetchJson(`/api/agent-drives/${id}`, { method: "DELETE" });

/** Deletes one service's data of an account (its scans and records), as a job. */
export const deleteServiceData = (
  clientKey: string,
  service: RecordedService
): Promise<DeletionJob> =>
  fetchJson(`/api/accounts/${encodeURIComponent(clientKey)}/${service}`, {
    method: "DELETE",
  });

/**
 * Disconnects an account and deletes everything recorded for it, as a
 * job; confirm is the account's name, as the user typed it.
 */
export const disconnectAccount = (
  clientKey: string,
  confirm: string
): Promise<DeletionJob> =>
  fetchJson(`/api/accounts/${encodeURIComponent(clientKey)}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ confirm }),
  });

/** A deletion job's status. */
export const getDeletion = (id: number): Promise<DeletionJob> =>
  fetchJson(`/api/deletions/${id}`);

/** The Duplicates page's summary: reclaimable space, counts, and when built. */
export const getDupSummary = (): Promise<DupSummary> =>
  fetchJson("/api/duplicates/summary");

/** A page (50) of groups of copies, largest reclaimable first. */
export const getDupGroups = (filter: DupFilter): Promise<DupGroupsPage> => {
  const params = new URLSearchParams({ kind: filter.kind });
  if (filter.source) {
    params.set("source", filter.source);
  }
  if (filter.across) {
    params.set("across", "1");
  }
  if (filter.min_size) {
    params.set("min_size", String(filter.min_size));
  }
  if (filter.hide_same_physical) {
    params.set("hide_same_physical", "1");
  }
  if (filter.page && filter.page > 1) {
    params.set("page", String(filter.page));
  }
  return fetchJson(`/api/duplicates/groups?${params}`);
};

/** A page (200) of one group's copies, the group named by kind and key. */
export const getDupMembers = (
  kind: DupKind,
  key: string,
  page: number
): Promise<DupMembersPage> =>
  fetchJson(
    `/api/duplicates/members?${new URLSearchParams({ kind, key, page: String(page) })}`
  );
