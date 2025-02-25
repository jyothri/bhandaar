import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { requestScan } from "../api";
import { ScanMetadata, ScanType } from "../types/scans";

type requestKey = {
  clientKey: string;
};

export const Route = createFileRoute("/request")({
  component: Request,
  validateSearch: (search: Record<string, unknown>): requestKey => {
    return {
      clientKey: (search.client_key as string) || "",
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
  const { clientKey } = Route.useSearch();
  const [scanClientKey, setScanClientKey] = useState("none");
  const [queryFilter, setQueryFilter] = useState("");
  const [errorMessage, setErrorMessage] = useState("");
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
      setErrorMessage("Select an account");
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
      },
    };
    try {
      await requestScanMutation(request);
    } catch (e) {
      console.log(e);
      setErrorMessage("Failed to submit request");
    }
  }

  return (
    <div className="flex flex-col">
      <div className="p-2">Make new Request!</div>
      <form>
        <div className="flex flex-row ml-2">
          <div className="flex flex-row ml-10">
            <input
              type="button"
              value="Link Google A/C"
              onClick={linkGoogleAccount}
            />
          </div>
        </div>
        <div className="flex flex-row ml-2">
          <div className="pl-3">
            <label htmlFor="scanClientKey">Accounts</label>
          </div>
          <div className="pl-3">
            <select
              id="scanClientKey"
              value={scanClientKey}
              onChange={(e) => setScanClientKey(e.target.value)}
            >
              <option value="none"> Select One </option>
              {clientKey && <option value={clientKey}> {clientKey} </option>}
            </select>
          </div>
        </div>
        <div className="flex flex-row ml-2">
          <div className="pl-3">
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
        </div>
        <div className="flex flex-row ml-10">
          <input type="button" value="Submit" onClick={submitRequest} />
        </div>
      </form>
      {errorMessage && <div className="text-red-500 h-1/5">{errorMessage}</div>}
    </div>
  );
}
