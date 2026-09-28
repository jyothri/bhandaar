// Labels for account lists. Display names are masked emails, so two Google
// accounts can share one; those get the start of their client key too.

type Named = { clientKey: string; displayName: string };

/** Each account's label, by client key. */
export function accountLabels(accounts: Named[]): Map<string, string> {
  const counts = new Map<string, number>();
  for (const { displayName } of accounts) {
    counts.set(displayName, (counts.get(displayName) ?? 0) + 1);
  }
  return new Map(
    accounts.map(({ clientKey, displayName }) => [
      clientKey,
      (counts.get(displayName) ?? 0) > 1
        ? `${displayName} · ${clientKey.slice(0, 4)}`
        : displayName,
    ])
  );
}
