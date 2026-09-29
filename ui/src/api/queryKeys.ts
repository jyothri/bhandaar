// TanStack Query keys, defined in one place so that invalidation can't
// drift from the keys the queries actually use.
export const queryKeys = {
  me: ["me"] as const,
  accounts: ["accounts"] as const,
  activePhotosPick: ["activePhotosPick"] as const,
  gcsProjects: (clientKey: string) => ["gcsProjects", clientKey] as const,
  gcsBuckets: (clientKey: string, project: string) =>
    ["gcsBuckets", clientKey, project] as const,
  gcsScanBuckets: (scanId: number) => ["gcsScanBuckets", scanId] as const,
  photosPick: (sessionKey: string) => ["photosPick", sessionKey] as const,
  scannedAccounts: ["scannedAccounts"] as const,
  // Prefix of every scanRequests(accountKey) key, for invalidating them all.
  allScanRequests: ["scanRequests"] as const,
  scanRequests: (accountKey: string) => ["scanRequests", accountKey] as const,
  scanSummary: (scanId: number) => ["scanSummary", scanId] as const,
  scanResults: (scanId: number, page: number) =>
    ["scanResults", scanId, page] as const,
  browseSources: ["browseSources"] as const,
  // source is "google:<client key>" or "agent:<drive ID>".
  browseChildren: (source: string, folder: string, page: number) =>
    ["browseChildren", source, folder, page] as const,
  agentStatus: (driveKey: string) => ["agentStatus", driveKey] as const,
  accountMessages: (clientKey: string, sort: string, page: number) =>
    ["accountMessages", clientKey, sort, page] as const,
  accountPhotos: (clientKey: string, sort: string, page: number) =>
    ["accountPhotos", clientKey, sort, page] as const,
  pickedItems: (scanId: number, page: number) =>
    ["pickedItems", scanId, page] as const,
};
