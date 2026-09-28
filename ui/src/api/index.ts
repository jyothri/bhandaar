import { config } from "../config";
import { Account } from "../types/accounts";
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

/**
 * Function to get list of accounts.
 */
export const getAccounts = (): Promise<Account[]> =>
  fetchJson("/api/accounts");

/**
 * Function to get accounts for which requests were submitted.
 */
export const getScannedAccounts = (): Promise<string[]> =>
  fetchJson("/api/scans/accounts");

/**
 * Function to get details for selected account.
 */
export const getScanRequests = (accountKey: string): Promise<ScanRequest[]> =>
  fetchJson(`/api/scans/requests/${encodeURIComponent(accountKey)}`);
