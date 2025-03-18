import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { backend_url, getScannedAccounts } from "../api";
import useSSE from "../components/hooks/useSse";
import { useState } from "react";

export const Route = createFileRoute("/requests")({
  component: Requests,
});

function Requests() {
  const [selectedAccount, setSelectedAccount] = useState("none");
  const [sseData, setSseData] = useState<string>("");

  const {
    data: scannedAccounts,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ["getScannedAccounts"],
    queryFn: () => getScannedAccounts(),
    staleTime: Infinity,
  });

  const { error: sseError } = useSSE(
    backend_url + "/sse/events",
    "timer",
    "close",
    setSseData
  );

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    setSelectedAccount(e.target.value);
  }

  return (
    <div>
      <h2 className="p-2 justify-self-center heading font-bold text-xl">
        Request history
      </h2>
      <div
        id="container"
        className="grid grid-cols-2 border-8 border-gray-200 gap-2"
      >
        {isLoading && (
          <div className="flex justify-center items-center sm:rounded-lg dark:text-gray-300">
            Fetching data..
          </div>
        )}
        {isError && (
          <div className="flex justify-center items-center sm:rounded-lg dark:text-gray-300">
            Error fetching data.
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
      <div>sseData: {sseData}</div>
      {sseError && <div>sseError: {sseError}</div>}
    </div>
  );
}
