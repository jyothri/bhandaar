import { useState, useEffect } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { requestScan, getAccounts } from "../api";
import { ScanMetadata, ScanType } from "../types/scans";

export const Route = createFileRoute("/request")({
  component: Request,
});

function Request() {
  const queryClient = useQueryClient();

  const [errorMessage, setErrorMessage] = useState("");
  const [infoMessage, setInfoMessage] = useState("");

  const [scanClientKey, setScanClientKey] = useState("none");
  const [username, setUsername] = useState("");
  const [inbox, setInbox] = useState(false);
  const [unread, setUnread] = useState(false);
  const [queryFilter, setQueryFilter] = useState("");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");

  const { data: accounts } = useQuery({
    queryKey: ["getAccounts"],
    queryFn: () => getAccounts(),
    staleTime: Infinity,
  });

  const { mutateAsync: requestScanMutation } = useMutation({
    mutationFn: requestScan,
    onSuccess: (resp) => {
      queryClient.invalidateQueries({ queryKey: ["scans"] });
      setInfoMessage("Request submitted successfully. ID: " + resp.scan_id);
    },
    onError: (error: any) => {
      console.log("got error response for addRequest", error);
    },
  });

  async function submitRequest() {
    if (scanClientKey === "none") {
      setErrorMessage("Please select an account.");
      return;
    }
    if (queryFilter === "") {
      setErrorMessage("Cannot submit request without any filter.");
      return;
    }
    const request: ScanMetadata = {
      ScanType: ScanType.GMail,
      GMailScan: {
        Filter: queryFilter,
        ClientKey: scanClientKey,
        RefreshToken: "",
        Username: username,
      },
    };
    try {
      await requestScanMutation(request);
    } catch (e) {
      console.log(e);
      setErrorMessage("Failed to submit request");
    }
  }

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    setScanClientKey(e.target.value);
    setUsername(e.target.selectedOptions[0].text);
  }

  const dateFromDatePicker = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.value === "") {
      return "";
    }
    const [year, month, day] = e.target.value.split("-").map(Number);
    const date = new Date(year, month - 1, day);
    return date.toISOString().split("T")[0];
  };

  const dateForApi = (input: string): string => {
    if (input === "") {
      return "";
    }
    const [year, month, day] = input.split("-").map(Number);
    return (
      year +
      "/" +
      (month < 10 ? "0" : "") +
      month +
      "/" +
      (day < 10 ? "0" : "") +
      day
    );
  };

  useEffect(() => {
    updateQueryFilter();
  }, [inbox, unread, startDate, endDate]);

  const updateQueryFilter = () => {
    let filter = "";
    if (inbox) {
      filter += "label:inbox ";
    }
    if (unread) {
      filter += "label:unread ";
    }
    if (startDate !== "") {
      filter += `after:${dateForApi(startDate)}  `;
    }
    if (endDate !== "") {
      filter += `before:${dateForApi(endDate)} `;
    }
    setQueryFilter(filter);
  };

  function linkGoogleAccount() {
    console.log("Linking Google Account");
    const spiUrl = "https://accounts.google.com/o/oauth2/v2/auth";
    const gmailScope = "https://www.googleapis.com/auth/gmail.readonly";
    const scope = `${gmailScope}`;
    const clientId =
      "112106509963-uluv01bacctqgd7mr003u7r1lpq3899n.apps.googleusercontent.com";
    const state = "YOUR_CUSTOM_STATE";
    const redirectUri = `${window.location.protocol}//${window.location.host}/oauth/glink`;
    const addtionalParams = "&access_type=offline&prompt=consent";
    const url = `${spiUrl}?response_type=code&scope=${scope}&client_id=${clientId}&state=${state}&redirect_uri=${redirectUri}${addtionalParams}`;
    window.location.href = url;
  }

  return (
    <div>
      <h2 className="p-2 justify-self-center heading font-bold text-xl">
        Make new Request
      </h2>
      <div
        id="container"
        className="grid grid-cols-2 border-8 border-gray-200 gap-2"
      >
        <div className="justify-self-center col-span-2">
          <button
            className="items-center justify-center py-2 px-4 rounded"
            value="Link Google A/C"
            onClick={linkGoogleAccount}
          >
            <img src="web_neutral_rd_ctn.svg" alt="Continue with Google" />
          </button>
        </div>
        <div className="justify-self-end pl-3">
          <label htmlFor="scanClientKey">Accounts</label>
        </div>
        <div className="pl-3">
          <select
            id="scanClientKey"
            value={scanClientKey}
            onChange={handleSelectAccount}
          >
            <option value="none">Select One</option>
            {accounts &&
              accounts.map((account) => (
                <option key={account.clientKey} value={account.clientKey}>
                  {account.displayName}
                </option>
              ))}
          </select>
        </div>

        <div className="justify-self-end pl-3 flex items-center">
          <label htmlFor="filter">Inbox</label>
        </div>
        <div className="pl-3">
          <input
            type="checkbox"
            id="inbox"
            name="inbox"
            checked={inbox}
            onChange={(e) => setInbox(e.target.checked)}
          />
        </div>

        <div className="justify-self-end pl-3 flex items-center">
          <label htmlFor="filter">Unread</label>
        </div>
        <div className="pl-3">
          <input
            type="checkbox"
            id="unread"
            name="unread"
            checked={unread}
            onChange={(e) => setUnread(e.target.checked)}
          />
        </div>

        <div className="justify-self-end pl-3 flex items-center">
          <label htmlFor="filter">Date range</label>
        </div>
        <div className="pl-3">
          <input
            id="datepicker-range-start"
            name="start"
            type="date"
            className="bg-gray-50 border border-gray-300 text-gray-900 text-sm rounded-lg focus:ring-blue-500 focus:border-blue-500 p-2.5  dark:bg-gray-700 dark:border-gray-600 dark:placeholder-gray-400 dark:text-white dark:focus:ring-blue-500 dark:focus:border-blue-500"
            placeholder="Select date start"
            value={startDate}
            onChange={(e) => {
              setStartDate(dateFromDatePicker(e));
            }}
          />
          <span className="mx-4 text-gray-500">to</span>
          <input
            id="datepicker-range-end"
            name="end"
            type="date"
            className="bg-gray-50 border border-gray-300 text-gray-900 text-sm rounded-lg focus:ring-blue-500 focus:border-blue-500 p-2.5  dark:bg-gray-700 dark:border-gray-600 dark:placeholder-gray-400 dark:text-white dark:focus:ring-blue-500 dark:focus:border-blue-500"
            placeholder="Select date end"
            value={endDate}
            onChange={(e) => {
              setEndDate(dateFromDatePicker(e));
            }}
          />
        </div>
        <div className="justify-self-end pl-3">
          <label htmlFor="filter">Query filter</label>
        </div>
        <div className="pl-3">
          <input
            id="filter"
            type="text"
            placeholder=""
            disabled={true}
            value={queryFilter}
            className="border-2 border-gray-200 rounded-lg w-10/12"
          />
        </div>
        <div className="justify-self-center col-span-2 p-3">
          <input
            className="items-center justify-center bg-blue-500 hover:bg-blue-700 text-white font-bold py-2 px-4 rounded"
            type="button"
            value="Submit"
            onClick={submitRequest}
          />
        </div>
        {errorMessage && (
          <div className="text-red-500 h-1/5 text-lg">{errorMessage}</div>
        )}
        {infoMessage && (
          <div className="text-blue-400 h-1/5 text-lg">{infoMessage}</div>
        )}
      </div>
    </div>
  );
}
