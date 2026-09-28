import { useEffect, useState } from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { requestScan, getAccounts } from "../api";
import { queryKeys } from "../api/queryKeys";
import { accountLabels } from "../accountLabels";
import {
  buildDriveQuery,
  driveFileTypes,
  driveFolderId,
  DriveFileType,
} from "../driveQuery";
import { buildGmailFilter } from "../gmailFilter";
import { linkGoogleAccount } from "../googleLink";
import { clearLinkService, linkService } from "../oauthState";
import { Service } from "../types/accounts";
import { ScanMetadata, ScanType } from "../types/scans";
import ScanProgress from "../components/ScanProgress";
import Input from "../components/Input";

type RequestSearch = {
  // The service to scan; Gmail when missing.
  type?: Service;
  // The account just linked, to select; set by the backend's redirect.
  account?: string;
};

export const Route = createFileRoute("/request")({
  component: Request,
  validateSearch: (search: Record<string, unknown>): RequestSearch => ({
    ...(search.type === "gmail" || search.type === "drive"
      ? { type: search.type }
      : {}),
    ...(typeof search.account === "string" && search.account !== ""
      ? { account: search.account }
      : {}),
  }),
});

const serviceNames: Record<Service, { full: string; short: string }> = {
  gmail: { full: "Gmail", short: "Gmail" },
  drive: { full: "Google Drive", short: "Drive" },
};

// The most a query can be; the backend records it in a VARCHAR(2000).
const MAX_QUERY_LENGTH = 2000;

type RequestForm = {
  clientKey: string;
  // Gmail
  inbox: boolean;
  unread: boolean;
  startDate: string;
  endDate: string;
  // Google Drive
  ownedByMe: boolean;
  includeTrash: boolean;
  fileTypes: DriveFileType[];
  modifiedFrom: string;
  modifiedTo: string;
  folder: string;
  recursive: boolean;
  // When set, rawQuery is sent instead of the query the fields build.
  editQuery: boolean;
  rawQuery: string;
};

const initialForm: RequestForm = {
  clientKey: "none",
  inbox: false,
  unread: false,
  startDate: "",
  endDate: "",
  ownedByMe: true,
  includeTrash: false,
  fileTypes: [],
  modifiedFrom: "",
  modifiedTo: "",
  folder: "",
  recursive: true,
  editQuery: false,
  rawQuery: "",
};

const labelCell = "justify-self-end pl-3 flex items-center";

function Request() {
  const queryClient = useQueryClient();
  const { type, account: linkedAccount } = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  // Back from linking, the service being linked (remembered before leaving
  // for Google) wins over the URL's, until the effect below settles it.
  const service: Service =
    (linkedAccount ? linkService() : null) ?? type ?? "gmail";

  // One message at a time, so a success and an error can't show together.
  const [message, setMessage] = useState<{
    kind: "error" | "info";
    text: string;
    // The scan a success message is about, to link to its results.
    scanId?: number;
  } | null>(null);

  // Back from linking, the account just linked starts out selected.
  const [form, setForm] = useState(() => ({
    ...initialForm,
    clientKey: linkedAccount ?? initialForm.clientKey,
  }));
  const updateForm = (changes: Partial<RequestForm>) =>
    setForm((current) => ({ ...current, ...changes }));
  const queryFilter = buildGmailFilter(form);
  const builtDriveQuery = buildDriveQuery({
    ...form,
    startDate: form.modifiedFrom,
    endDate: form.modifiedTo,
  });
  const driveQuery = form.editQuery ? form.rawQuery : builtDriveQuery;

  const { data: accounts } = useQuery({
    queryKey: queryKeys.accounts,
    queryFn: () => getAccounts(),
    staleTime: Infinity,
  });

  // The selected account, if it's (still) one of the user's.
  const account = accounts?.find((a) => a.clientKey === form.clientKey);
  const labels = accountLabels(accounts ?? []);
  const missingService = account && !account.services.includes(service);

  // Once back from linking, put the service in the URL and drop the
  // account, so a reload doesn't undo a later choice.
  useEffect(() => {
    if (linkedAccount) {
      navigate({ search: { type: service }, replace: true });
      clearLinkService();
    }
  }, [linkedAccount, service, navigate]);

  const { mutate: requestScanMutation, isPending } = useMutation({
    mutationFn: requestScan,
    onSuccess: (resp) => {
      // The new scan belongs in the history, and its account may be new there.
      queryClient.invalidateQueries({ queryKey: queryKeys.allScanRequests });
      queryClient.invalidateQueries({ queryKey: queryKeys.scannedAccounts });
      setMessage({
        kind: "info",
        text: "Request submitted successfully. ID: " + resp.scan_id,
        scanId: resp.scan_id,
      });
    },
    onError: (error) => {
      setMessage({
        kind: "error",
        text: `Failed to submit request: ${error.message}`,
      });
    },
  });

  function selectService(next: Service) {
    setMessage(null);
    navigate({ search: { type: next }, replace: true });
  }

  function gmailRequest(clientKey: string): ScanMetadata | string {
    if (queryFilter === "") {
      return "Cannot submit request without any filter.";
    }
    // YYYY-MM-DD strings compare in date order.
    if (form.startDate && form.endDate && form.endDate < form.startDate) {
      return "The end date is before the start date.";
    }
    return {
      ScanType: ScanType.GMail,
      GMailScan: { Filter: queryFilter, ClientKey: clientKey, RefreshToken: "" },
    };
  }

  function driveRequest(clientKey: string): ScanMetadata | string {
    if (
      !form.editQuery &&
      form.modifiedFrom &&
      form.modifiedTo &&
      form.modifiedTo < form.modifiedFrom
    ) {
      return "The end date is before the start date.";
    }
    if (driveQuery.length > MAX_QUERY_LENGTH) {
      return `The query is too long: ${driveQuery.length} characters, the most is ${MAX_QUERY_LENGTH}.`;
    }
    const folderId = driveFolderId(form.folder);
    if (folderId === null) {
      return "That isn't a Google Drive folder link or ID.";
    }
    return {
      ScanType: ScanType.GDrive,
      GDriveScan: {
        QueryString: driveQuery,
        FolderId: folderId,
        Recursive: folderId !== "" && form.recursive,
        ClientKey: clientKey,
        RefreshToken: "",
      },
    };
  }

  function submitRequest() {
    if (!account) {
      setMessage({ kind: "error", text: "Please select an account." });
      return;
    }
    const request =
      service === "drive"
        ? driveRequest(account.clientKey)
        : gmailRequest(account.clientKey);
    if (typeof request === "string") {
      setMessage({ kind: "error", text: request });
      return;
    }
    setMessage(null);
    requestScanMutation(request);
  }

  function handleSelectAccount(e: React.ChangeEvent<HTMLSelectElement>) {
    updateForm({ clientKey: e.target.value });
  }

  function toggleFileType(fileType: DriveFileType, checked: boolean) {
    setForm((current) => ({
      ...current,
      fileTypes: checked
        ? [...current.fileTypes, fileType]
        : current.fileTypes.filter((t) => t !== fileType),
    }));
  }

  // Editing starts from the query the fields build.
  function toggleEditQuery(checked: boolean) {
    updateForm({ editQuery: checked, rawQuery: builtDriveQuery });
  }

  const { full, short } = serviceNames[service];

  return (
    <div>
      <h2 className="p-2 justify-self-center heading font-bold text-xl">
        Make new Request
      </h2>
      <div
        id="container"
        className="grid grid-cols-2 border-8 border-gray-200 dark:border-gray-700 gap-2"
      >
        <fieldset className="justify-self-center col-span-2 flex gap-4 p-2">
          <legend className="sr-only">Scan</legend>
          <span>Scan:</span>
          {(["gmail", "drive"] as const).map((s) => (
            <label key={s} className="flex items-center gap-1">
              <input
                type="radio"
                name="service"
                value={s}
                checked={service === s}
                onChange={() => selectService(s)}
              />
              {serviceNames[s].full}
            </label>
          ))}
        </fieldset>

        <div className="justify-self-end pl-3">
          <label htmlFor="scanClientKey">Accounts</label>
        </div>
        <div className="pl-3 flex flex-wrap items-center gap-2">
          <select
            id="scanClientKey"
            value={account?.clientKey ?? "none"}
            onChange={handleSelectAccount}
          >
            <option value="none">Select One</option>
            {accounts &&
              accounts.map((account) => (
                <option key={account.clientKey} value={account.clientKey}>
                  {labels.get(account.clientKey)}
                </option>
              ))}
          </select>
          <button
            type="button"
            className="underline"
            onClick={() => linkGoogleAccount(service)}
          >
            Link another Google account
          </button>
        </div>

        {missingService && (
          <div className="col-span-2 justify-self-center p-2" role="status">
            This account hasn't granted {full} access.{" "}
            <button
              type="button"
              className="bg-blue-500 hover:bg-blue-700 text-white font-bold py-1 px-3 rounded"
              onClick={() => linkGoogleAccount(service, account.loginHint)}
            >
              Grant {short} access
            </button>
          </div>
        )}

        {!missingService && service === "gmail" && (
          <>
            <div className={labelCell}>
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

            <div className={labelCell}>
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

            <div className={labelCell}>
              <label htmlFor="datepicker-range-start">Date range</label>
            </div>
            <div className="pl-3">
              <Input
                id="datepicker-range-start"
                name="start"
                type="date"
                placeholder="Select date start"
                value={form.startDate}
                onChange={(e) => updateForm({ startDate: e.target.value })}
              />
              <span className="mx-4 text-gray-500">to</span>
              <Input
                id="datepicker-range-end"
                name="end"
                type="date"
                placeholder="Select date end"
                value={form.endDate}
                onChange={(e) => updateForm({ endDate: e.target.value })}
              />
            </div>
            <div className="justify-self-end pl-3">
              <label htmlFor="filter">Query filter</label>
            </div>
            <div className="pl-3">
              <Input
                id="filter"
                type="text"
                disabled={true}
                value={queryFilter}
                className="w-10/12"
              />
            </div>
          </>
        )}

        {!missingService && service === "drive" && (
          <>
            <div className={labelCell}>
              <label htmlFor="ownedByMe">Owned by me</label>
            </div>
            <div className="pl-3">
              <input
                type="checkbox"
                id="ownedByMe"
                checked={form.ownedByMe}
                disabled={form.editQuery}
                onChange={(e) => updateForm({ ownedByMe: e.target.checked })}
              />
            </div>

            <div className={labelCell}>
              <span id="fileTypesLabel">File types</span>
            </div>
            <div
              className="pl-3 flex flex-wrap gap-3"
              role="group"
              aria-labelledby="fileTypesLabel"
            >
              {driveFileTypes.map(({ value, label }) => (
                <label key={value} className="flex items-center gap-1">
                  <input
                    type="checkbox"
                    checked={form.fileTypes.includes(value)}
                    disabled={form.editQuery}
                    onChange={(e) => toggleFileType(value, e.target.checked)}
                  />
                  {label}
                </label>
              ))}
            </div>

            <div className={labelCell}>
              <label htmlFor="drive-modified-start">Modified</label>
            </div>
            <div className="pl-3">
              <Input
                id="drive-modified-start"
                type="date"
                value={form.modifiedFrom}
                disabled={form.editQuery}
                onChange={(e) => updateForm({ modifiedFrom: e.target.value })}
              />
              <span className="mx-4 text-gray-500">to</span>
              <Input
                id="drive-modified-end"
                aria-label="Modified to"
                type="date"
                value={form.modifiedTo}
                disabled={form.editQuery}
                onChange={(e) => updateForm({ modifiedTo: e.target.value })}
              />
              <span className="ml-2 text-gray-500">(UTC)</span>
            </div>

            <div className={labelCell}>
              <label htmlFor="includeTrash">Include trash</label>
            </div>
            <div className="pl-3">
              <input
                type="checkbox"
                id="includeTrash"
                checked={form.includeTrash}
                disabled={form.editQuery}
                onChange={(e) => updateForm({ includeTrash: e.target.checked })}
              />
            </div>

            <div className={labelCell}>
              <label htmlFor="driveFolder">Folder</label>
            </div>
            <div className="pl-3 flex flex-wrap items-center gap-3">
              <Input
                id="driveFolder"
                type="text"
                placeholder="Paste a folder link or ID; empty for the whole Drive"
                value={form.folder}
                onChange={(e) => updateForm({ folder: e.target.value })}
                className="w-8/12"
              />
              {form.folder.trim() !== "" && (
                <label className="flex items-center gap-1">
                  <input
                    type="checkbox"
                    checked={form.recursive}
                    onChange={(e) =>
                      updateForm({ recursive: e.target.checked })
                    }
                  />
                  Include subfolders
                </label>
              )}
            </div>

            <div className="justify-self-end pl-3">
              <label htmlFor="driveQuery">Query</label>
            </div>
            <div className="pl-3">
              <Input
                id="driveQuery"
                type="text"
                disabled={!form.editQuery}
                value={driveQuery}
                onChange={(e) => updateForm({ rawQuery: e.target.value })}
                className="w-10/12"
              />
              <label className="flex items-center gap-1 pt-1">
                <input
                  type="checkbox"
                  checked={form.editQuery}
                  onChange={(e) => toggleEditQuery(e.target.checked)}
                />
                Edit query
              </label>
            </div>
          </>
        )}

        <div className="justify-self-center col-span-2 p-3">
          <input
            className="items-center justify-center bg-blue-500 hover:bg-blue-700 disabled:bg-blue-300 disabled:cursor-not-allowed text-white font-bold py-2 px-4 rounded"
            type="button"
            value={isPending ? "Submitting…" : "Submit"}
            disabled={isPending || missingService}
            onClick={submitRequest}
          />
        </div>
      </div>
      {message && (
        <div
          className={`${message.kind === "error" ? "text-red-500" : "text-blue-400"} h-1/5 text-lg`}
        >
          <span>{message.text}</span>
          {message.scanId !== undefined && (
            <>
              {" "}
              <Link
                to="/scans/$scanId"
                params={{ scanId: String(message.scanId) }}
                search={{ page: 1 }}
                className="underline"
              >
                View results
              </Link>
            </>
          )}
        </div>
      )}

      <ScanProgress />
    </div>
  );
}
