import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { requestScan } from "../api";
import { ScanMetadata, ScanType } from "../types/scans";

type requestKey = {
  clientKey: string;
  displayName: string;
};

export const Route = createFileRoute("/request")({
  component: Request,
  validateSearch: (search: Record<string, unknown>): requestKey => {
    return {
      clientKey: (search.client_key as string) || "",
      displayName: (search.display_name as string) || "",
    };
  },
});

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

function Request() {
  const { clientKey, displayName } = Route.useSearch();
  const [scanClientKey, setScanClientKey] = useState("none");
  const [queryFilter, setQueryFilter] = useState("");
  const [errorMessage, setErrorMessage] = useState("");
  const [infoMessage, setInfoMessage] = useState("");
  const queryClient = useQueryClient();

  const { mutateAsync: requestScanMutation } = useMutation({
    mutationFn: requestScan,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["scans"] });
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
      setErrorMessage("Enter a query filter");
      return;
    }
    console.log(
      "Submitting Request with setScanClientKey:%s, query filter: %s",
      scanClientKey,
      queryFilter
    );

    const request: ScanMetadata = {
      ScanType: ScanType.GMail,
      GMailScan: {
        Filter: queryFilter,
        ClientKey: scanClientKey,
        RefreshToken: "",
        Username: displayName,
      },
    };
    try {
      await requestScanMutation(request);
      setInfoMessage("Request submitted successfully");
    } catch (e) {
      console.log(e);
      setErrorMessage("Failed to submit request");
    }
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
            onChange={(e) => setScanClientKey(e.target.value)}
          >
            <option value="none"> Select One </option>
            {clientKey && <option value={clientKey}> {displayName} </option>}
          </select>
        </div>
        <div className="justify-self-end pl-3">
          <label htmlFor="filter">Query filter</label>
        </div>
        <div className="pl-3">
          <input
            id="filter"
            type="text"
            placeholder="label:inbox label:unread"
            value={queryFilter}
            onChange={(e) => setQueryFilter(e.target.value)}
          />
        </div>
        <div className="justify-self-center col-span-2">
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
