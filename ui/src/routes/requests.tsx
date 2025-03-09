import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { getScannedAccounts } from "../api";
import { useState } from "react";

export const Route = createFileRoute("/requests")({
  component: Requests,
});

function Requests() {
  const {
    data: scannedAccounts,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ["getScannedAccounts"],
    queryFn: () => getScannedAccounts(),
    staleTime: Infinity,
  });
  const [selectedAccount, setSelectedAccount] = useState("none");

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    setSelectedAccount(e.target.value);
  }

  return isLoading ? (
    <div className="flex justify-center items-center sm:rounded-lg dark:text-gray-300">
      Fetching data..
    </div>
  ) : (
    <div className="flex justify-center items-center shadow-md sm:rounded-lg">
      {isError && (
        <div className="flex justify-center items-center shadow-md sm:rounded-lg dark:text-gray-400">
          Error fetching data.
        </div>
      )}
      <div className="justify-self-end pl-3">
        <label htmlFor="scanClientKey">Select an account</label>
      </div>
      <div className="pl-3">
        <select
          id="scanClientKey"
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
  );
}
