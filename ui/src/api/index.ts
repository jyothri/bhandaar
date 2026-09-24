import { config } from "../config";
import { Account } from "../types/accounts";
import { RequestScanResponse, ScanMetadata, ScanRequest } from "../types/scans";

export const backend_url = config.backendUrl;

/**
 * Fetches a backend path and parses the JSON response. Throws an Error with
 * the backend's message if the response isn't 2xx.
 */
async function fetchJson<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(backend_url + path, init);
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
  return response.json();
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
