import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { getScannedAccounts, getScanRequests } from "../api";
import { queryKeys } from "../api/queryKeys";
import { formatDateTime, formatDuration } from "../format";
import { Table, Td, Tr } from "../components/Table";
import { useState } from "react";

export const Route = createFileRoute("/requests")({
  component: Requests,
});

// The backend sends "-1" for a scan without an end time.
function scanDuration(seconds: string): string {
  const value = Number(seconds);
  return value < 0 ? "Not finished" : formatDuration(value);
}

function Requests() {
  const [selectedAccount, setSelectedAccount] = useState("none");

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
    // scan is unfinished.
    refetchOnWindowFocus: true,
    refetchInterval: (query) =>
      query.state.data?.some((scan) => Number(scan.scan_duration_in_sec) < 0)
        ? 10_000
        : false,
  });

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    setSelectedAccount(e.target.value);
  }

  return (
    <div>
      <h2 className="p-2 justify-self-center heading font-bold text-xl">
        Request history
      </h2>
      <div id="container" className="border-8 border-gray-200 gap-2">
        <div className="grid grid-cols-2 ">
          {isLoading && (
            <div className="flex justify-center items-center sm:rounded-lg dark:text-gray-300">
              Fetching data..
            </div>
          )}
          {accountsError && (
            <div className="flex justify-center items-center sm:rounded-lg text-red-500">
              Couldn't load accounts: {accountsError.message}
            </div>
          )}
          <div className="justify-self-end pl-3">
            <label htmlFor="selectAccount">Select an account</label>
          </div>
          <div className="pl-3">
            <select
              id="selectAccount"
              value={selectedAccount}
              onChange={handleSelectAccount}
            >
              <option value="none">Select One</option>
              {scannedAccounts &&
                scannedAccounts.map((account) => (
                  <option key={account} value={account}>
                    {account}
                  </option>
                ))}
            </select>
          </div>
        </div>
        {scanRequestsLoading && <p className="p-3">Loading scans…</p>}
        {scanRequestsError && (
          <p className="p-3 text-red-500">
            Couldn't load scans: {scanRequestsError.message}
          </p>
        )}
        {scanRequests?.length === 0 && (
          <p className="p-3">No scans for this account.</p>
        )}
        {scanRequests !== undefined && scanRequests.length > 0 && (
          <Table
            className="w-7/8"
            headers={[
              "Name",
              "Scan Type",
              "Scan id",
              "Search Filter",
              "Scan start",
              "Duration",
            ]}
          >
            {scanRequests.map((scanRequest) => (
              <Tr key={scanRequest.scan_id}>
                <Td>{scanRequest.name}</Td>
                <Td>{scanRequest.scan_type}</Td>
                <Td>{scanRequest.scan_id}</Td>
                <Td>{scanRequest.search_filter}</Td>
                <Td>{formatDateTime(scanRequest.scan_start_time)}</Td>
                <Td>{scanDuration(scanRequest.scan_duration_in_sec)}</Td>
              </Tr>
            ))}
          </Table>
        )}
      </div>
    </div>
  );
}
