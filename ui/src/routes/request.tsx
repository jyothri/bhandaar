import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { requestScan, getAccounts } from "../api";
import { queryKeys } from "../api/queryKeys";
import { config } from "../config";
import { createOAuthState } from "../oauthState";
import { ScanMetadata, ScanType } from "../types/scans";
import ScanProgress from "../components/ScanProgress";

export const Route = createFileRoute("/request")({
  component: Request,
});

type RequestForm = {
  clientKey: string;
  username: string;
  inbox: boolean;
  unread: boolean;
  startDate: string;
  endDate: string;
};

const initialForm: RequestForm = {
  clientKey: "none",
  username: "",
  inbox: false,
  unread: false,
  startDate: "",
  endDate: "",
};

// Formats a YYYY-MM-DD date as Gmail's YYYY/MM/DD, shifted by `days`.
function dateForApi(input: string, days = 0): string {
  const [year, month, day] = input.split("-").map(Number);
  const date = new Date(Date.UTC(year, month - 1, day + days));
  return date.toISOString().slice(0, 10).replace(/-/g, "/");
}

function buildGmailFilter({
  inbox,
  unread,
  startDate,
  endDate,
}: RequestForm): string {
  let filter = "";
  if (inbox) {
    filter += "label:inbox ";
  }
  if (unread) {
    filter += "is:unread ";
  }
  if (startDate !== "") {
    filter += `after:${dateForApi(startDate)} `;
  }
  if (endDate !== "") {
    // Gmail's before: is exclusive; use the next day to include endDate.
    filter += `before:${dateForApi(endDate, 1)} `;
  }
  return filter;
}

function Request() {
  const queryClient = useQueryClient();

  const [errorMessage, setErrorMessage] = useState("");
  const [infoMessage, setInfoMessage] = useState("");

  const [form, setForm] = useState(initialForm);
  const updateForm = (changes: Partial<RequestForm>) =>
    setForm((current) => ({ ...current, ...changes }));
  const queryFilter = buildGmailFilter(form);

  const { data: accounts } = useQuery({
    queryKey: queryKeys.accounts,
    queryFn: () => getAccounts(),
    staleTime: Infinity,
  });

  const { mutateAsync: requestScanMutation } = useMutation({
    mutationFn: requestScan,
    onSuccess: (resp) => {
      // The new scan belongs in the history, and its account may be new there.
      queryClient.invalidateQueries({ queryKey: queryKeys.allScanRequests });
      queryClient.invalidateQueries({ queryKey: queryKeys.scannedAccounts });
      setErrorMessage("");
      setInfoMessage("Request submitted successfully. ID: " + resp.scan_id);
    },
    onError: (error: any) => {
      console.log("got error response for addRequest", error);
    },
  });

  async function submitRequest() {
    if (form.clientKey === "none") {
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
        ClientKey: form.clientKey,
        RefreshToken: "",
        Username: form.username,
      },
    };
    try {
      await requestScanMutation(request);
    } catch (e) {
      console.log(e);
      setErrorMessage(
        e instanceof Error
          ? `Failed to submit request: ${e.message}`
          : "Failed to submit request"
      );
    }
  }

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    updateForm({
      clientKey: e.target.value,
      username: e.target.selectedOptions[0].text,
    });
  }

  function linkGoogleAccount() {
    console.log("Linking Google Account");
    const spiUrl = "https://accounts.google.com/o/oauth2/v2/auth";
    const gmailScope = "https://www.googleapis.com/auth/gmail.readonly";
    const params = new URLSearchParams({
      response_type: "code",
      scope: gmailScope,
      client_id: config.googleClientId,
      state: createOAuthState(),
      redirect_uri: `${window.location.origin}/oauth/glink`,
      access_type: "offline",
      prompt: "consent",
    });
    window.location.href = `${spiUrl}?${params}`;
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
            value={form.clientKey}
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
          <label htmlFor="inbox">Inbox</label>
        </div>
        <div className="pl-3">
          <input
            type="checkbox"
            id="inbox"
            name="inbox"
            checked={form.inbox}
            onChange={(e) => updateForm({ inbox: e.target.checked })}
          />
        </div>

        <div className="justify-self-end pl-3 flex items-center">
          <label htmlFor="unread">Unread</label>
        </div>
        <div className="pl-3">
          <input
            type="checkbox"
            id="unread"
            name="unread"
            checked={form.unread}
            onChange={(e) => updateForm({ unread: e.target.checked })}
          />
        </div>

        <div className="justify-self-end pl-3 flex items-center">
          <label htmlFor="datepicker-range-start">Date range</label>
        </div>
        <div className="pl-3">
          <input
            id="datepicker-range-start"
            name="start"
            type="date"
            className="bg-gray-50 border border-gray-300 text-gray-900 text-sm rounded-lg focus:ring-blue-500 focus:border-blue-500 p-2.5  dark:bg-gray-700 dark:border-gray-600 dark:placeholder-gray-400 dark:text-white dark:focus:ring-blue-500 dark:focus:border-blue-500"
            placeholder="Select date start"
            value={form.startDate}
            onChange={(e) => updateForm({ startDate: e.target.value })}
          />
          <span className="mx-4 text-gray-500">to</span>
          <input
            id="datepicker-range-end"
            name="end"
            type="date"
            className="bg-gray-50 border border-gray-300 text-gray-900 text-sm rounded-lg focus:ring-blue-500 focus:border-blue-500 p-2.5  dark:bg-gray-700 dark:border-gray-600 dark:placeholder-gray-400 dark:text-white dark:focus:ring-blue-500 dark:focus:border-blue-500"
            placeholder="Select date end"
            value={form.endDate}
            onChange={(e) => updateForm({ endDate: e.target.value })}
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
      </div>
      {errorMessage && (
        <div className="text-red-500 h-1/5 text-lg">{errorMessage}</div>
      )}
      {infoMessage && (
        <div className="text-blue-400 h-1/5 text-lg">{infoMessage}</div>
      )}

      <ScanProgress />
    </div>
  );
}
