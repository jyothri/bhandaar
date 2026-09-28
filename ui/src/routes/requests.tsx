import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { getScannedAccounts, getScanRequests } from "../api";
import { queryKeys } from "../api/queryKeys";
import { formatDateTime, formatDuration, scanTypeLabel } from "../format";
import ScanStatus from "../components/ScanStatus";
import Card from "../components/ui/Card";
import Field from "../components/ui/Field";
import Select from "../components/ui/Select";
import Table from "../components/ui/Table";
import { accountLabels } from "../accountLabels";
import Breadcrumbs from "../components/Breadcrumbs";
import { ScanRequest } from "../types/scans";

type RequestsSearch = {
  // The selected account's client key, so links (and Back) can return to it.
  account?: string;
};

export const Route = createFileRoute("/requests")({
  component: Requests,
  validateSearch: (search: Record<string, unknown>): RequestsSearch =>
    typeof search.account === "string" && search.account !== ""
      ? { account: search.account }
      : {},
});

// Stop polling for a scan that has been open this long; it's stuck.
const MAX_POLL_AGE_MS = 6 * 60 * 60 * 1000;

// The backend sends "-1" for a scan without an end time.
function scanDuration(scan: ScanRequest): string {
  const seconds = Number(scan.scan_duration_in_sec);
  return seconds < 0 ? "—" : formatDuration(seconds);
}

// A scan that may still finish: no end time, not failed, and recent.
function mayStillFinish(scan: ScanRequest): boolean {
  return (
    Number(scan.scan_duration_in_sec) < 0 &&
    scan.status !== "Failed" &&
    Date.now() - Date.parse(scan.scan_start_time) < MAX_POLL_AGE_MS
  );
}

function Requests() {
  const { account } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const selectedAccount = account ?? "none";

  const {
    data: scannedAccounts,
    isLoading,
    error: accountsError,
  } = useQuery({
    queryKey: queryKeys.scannedAccounts,
    queryFn: () => getScannedAccounts(),
    staleTime: Infinity,
  });

  const {
    data: scanRequests,
    isLoading: scanRequestsLoading,
    error: scanRequestsError,
  } = useQuery({
    queryKey: queryKeys.scanRequests(selectedAccount),
    queryFn: () => getScanRequests(selectedAccount),
    enabled: selectedAccount !== "none",
    // A running scan's duration changes when it finishes, so don't keep
    // this list forever: refetch on mount and focus, and poll while any
    // scan may still finish.
    refetchOnWindowFocus: true,
    refetchInterval: (query) =>
      query.state.data?.some(mayStillFinish) ? 10_000 : false,
  });

  const labels = accountLabels(scannedAccounts ?? []);

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    const next = e.target.value;
    navigate({ search: next === "none" ? {} : { account: next } });
  }

  const accountLabel = account && labels.get(account);

  return (
    <div>
      <Breadcrumbs
        items={
          accountLabel
            ? [
                <Link to="/requests" search={{}}>
                  Request History
                </Link>,
                accountLabel,
              ]
            : ["Request History"]
        }
      />
      <div className="space-y-4">
        <h1 className="text-xl font-semibold">Request History</h1>
        <Card>
          <Field label="Select an account" id="selectAccount" inline>
            {(control) => (
              <Select
                {...control}
                value={selectedAccount}
                onChange={handleSelectAccount}
                disabled={isLoading}
                className="w-full sm:w-80"
              >
                <option value="none">
                  {isLoading ? "Loading accounts…" : "Select one"}
                </option>
                {scannedAccounts &&
                  scannedAccounts.map((account) => (
                    <option key={account.clientKey} value={account.clientKey}>
                      {labels.get(account.clientKey)}
                    </option>
                  ))}
              </Select>
            )}
          </Field>
          {accountsError && (
            <p className="mt-3 text-sm text-danger">
              Couldn't load accounts: {accountsError.message}
            </p>
          )}
        </Card>

        {selectedAccount !== "none" && (
          <Card title="Scans">
            {scanRequestsLoading && (
              <p className="text-sm text-muted">Loading scans…</p>
            )}
            {scanRequestsError && (
              <p className="text-sm text-danger">
                Couldn't load scans: {scanRequestsError.message}
              </p>
            )}
            {scanRequests?.length === 0 && (
              <p className="text-sm text-muted">No scans for this account.</p>
            )}
            {scanRequests !== undefined && scanRequests.length > 0 && (
              <Table
                rowKey={(r) => r.scan_id}
                rows={scanRequests}
                columns={[
                  {
                    header: "Scan",
                    primary: true,
                    cell: (r) => (
                      <>
                        <span className="sm:hidden">Scan </span>
                        <Link
                          to="/scans/$scanId"
                          params={{ scanId: String(r.scan_id) }}
                          search={{ page: 1 }}
                          className="font-medium text-accent hover:underline"
                        >
                          {r.scan_id}
                        </Link>
                      </>
                    ),
                  },
                  { header: "Type", cell: (r) => scanTypeLabel(r.scan_type) },
                  {
                    header: "Filter",
                    cell: (r) => (
                      <span className="font-mono text-xs wrap-anywhere">
                        {r.search_path
                          ? `${r.search_path}: ${r.search_filter}`
                          : r.search_filter}
                      </span>
                    ),
                    className: "sm:max-w-md",
                  },
                  {
                    header: "Started",
                    cell: (r) => formatDateTime(r.scan_start_time),
                    className: "sm:whitespace-nowrap",
                  },
                  { header: "Duration", numeric: true, cell: scanDuration },
                  {
                    header: "Status",
                    cell: (r) => <ScanStatus status={r.status} />,
                  },
                ]}
              />
            )}
          </Card>
        )}
      </div>
    </div>
  );
}
