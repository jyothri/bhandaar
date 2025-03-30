import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { getScannedAccounts, getScanRequests } from "../api";
import { useState } from "react";

export const Route = createFileRoute("/requests")({
  component: Requests,
});

function Requests() {
  const [selectedAccount, setSelectedAccount] = useState("none");

  const {
    data: scannedAccounts,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ["getScannedAccounts"],
    queryFn: () => getScannedAccounts(),
    staleTime: Infinity,
  });

  const { data: scanRequests } = useQuery({
    queryKey: ["getScanRequests", selectedAccount],
    queryFn: () => getScanRequests(selectedAccount),
    staleTime: Infinity,
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
        {scanRequests !== undefined && scanRequests?.length > 0 && (
          <table className="w-7/8 mt-3 text-sm text-left rtl:text-right text-gray-500 dark:text-gray-400 justify-self-center">
            <thead>
              <tr className="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
                <th scope="col" className="px-6 py-3">
                  Name
                </th>
                <th scope="col" className="px-6 py-3">
                  Scan Type
                </th>
                <th scope="col" className="px-6 py-3">
                  Scan id
                </th>
                <th scope="col" className="px-6 py-3">
                  Search Filter
                </th>
                <th scope="col" className="px-6 py-3">
                  Scan start
                </th>
                <th scope="col" className="px-6 py-3">
                  Processing Time (sec)
                </th>
              </tr>
            </thead>
            <tbody>
              {scanRequests?.map((scanRequest) => (
                <tr
                  key={scanRequest.scan_id}
                  className="odd:bg-white odd:dark:bg-gray-900 even:bg-gray-50 even:dark:bg-gray-800 border-b dark:border-gray-700 border-gray-200"
                >
                  <td className="px-6 py-4">{scanRequest.name}</td>
                  <td className="px-6 py-4">{scanRequest.scan_type}</td>
                  <td className="px-6 py-4">{scanRequest.scan_id}</td>
                  <td className="px-6 py-4">{scanRequest.search_filter}</td>
                  <td className="px-6 py-4">{scanRequest.scan_start_time}</td>
                  <td className="px-6 py-4">
                    {scanRequest.scan_duration_in_sec}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
