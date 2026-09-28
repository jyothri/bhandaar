// TanStack Query keys, defined in one place so that invalidation can't
// drift from the keys the queries actually use.
export const queryKeys = {
  me: ["me"] as const,
  accounts: ["accounts"] as const,
  scannedAccounts: ["scannedAccounts"] as const,
  // Prefix of every scanRequests(accountKey) key, for invalidating them all.
  allScanRequests: ["scanRequests"] as const,
  scanRequests: (accountKey: string) => ["scanRequests", accountKey] as const,
  scanSummary: (scanId: number) => ["scanSummary", scanId] as const,
  scanResults: (scanId: number, page: number) =>
    ["scanResults", scanId, page] as const,
};
