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
import PhotosPick from "../components/PhotosPick";
import ScanProgress from "../components/ScanProgress";
import Button from "../components/ui/Button";
import Card from "../components/ui/Card";
import Checkbox from "../components/ui/Checkbox";
import Field, { FieldGroup } from "../components/ui/Field";
import Icon from "../components/ui/Icon";
import Input from "../components/ui/Input";
import Select from "../components/ui/Select";
import Tabs from "../components/ui/Tabs";

type RequestSearch = {
  // The service to scan; Gmail when missing.
  type?: Service;
  // The account just linked, to select; set by the backend's redirect.
  account?: string;
};

export const Route = createFileRoute("/request")({
  component: Request,
  validateSearch: (search: Record<string, unknown>): RequestSearch => ({
    ...(search.type === "gmail" ||
    search.type === "drive" ||
    search.type === "photos"
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
  photos: { full: "Google Photos", short: "Photos" },
  gcs: { full: "Google Cloud Storage", short: "Cloud Storage" },
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
      GMailScan: {
        Filter: queryFilter,
        ClientKey: clientKey,
        RefreshToken: "",
      },
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
  const dateRange = (
    from: { id: string; value: string; label?: string },
    to: { id: string; value: string; label: string },
    onFrom: (v: string) => void,
    onTo: (v: string) => void,
    disabled = false
  ) => (
    <div className="flex flex-wrap items-center gap-2">
      <Input
        id={from.id}
        type="date"
        value={from.value}
        disabled={disabled}
        onChange={(e) => onFrom(e.target.value)}
        className="w-auto flex-1 sm:w-44 sm:flex-none"
      />
      <span className="text-sm text-muted">to</span>
      <Input
        id={to.id}
        aria-label={to.label}
        type="date"
        value={to.value}
        disabled={disabled}
        onChange={(e) => onTo(e.target.value)}
        className="w-auto flex-1 sm:w-44 sm:flex-none"
      />
    </div>
  );

  return (
    <div className="space-y-4 pt-6">
      <h1 className="text-xl font-semibold">New request</h1>
      <Card>
        <Tabs<Service>
          label="Scan"
          value={service}
          onChange={selectService}
          items={[
            { id: "gmail", label: serviceNames.gmail.full },
            { id: "drive", label: serviceNames.drive.full },
            { id: "photos", label: serviceNames.photos.full },
          ]}
        />
        <div className="mt-5 grid gap-5">
          <Field label="Accounts" inline>
            {(control) => (
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <Select
                  {...control}
                  value={account?.clientKey ?? "none"}
                  onChange={handleSelectAccount}
                  className="w-full sm:w-80"
                >
                  <option value="none">Select one</option>
                  {accounts &&
                    accounts.map((account) => (
                      <option key={account.clientKey} value={account.clientKey}>
                        {labels.get(account.clientKey)}
                      </option>
                    ))}
                </Select>
                <Button
                  variant="secondary"
                  onClick={() => linkGoogleAccount(service)}
                >
                  Link another Google account
                </Button>
              </div>
            )}
          </Field>

          {missingService && (
            <div
              role="status"
              className="flex flex-col items-start gap-3 rounded-md bg-accent-soft px-4 py-3 text-sm sm:flex-row sm:items-center sm:justify-between"
            >
              <span>This account hasn't granted {full} access.</span>
              <Button
                onClick={() => linkGoogleAccount(service, account.loginHint)}
              >
                Grant {short} access
              </Button>
            </div>
          )}

          {!missingService && service === "gmail" && (
            <>
              <FieldGroup label="Messages">
                <Checkbox
                  id="inbox"
                  name="inbox"
                  label="Inbox"
                  checked={form.inbox}
                  onChange={(e) => updateForm({ inbox: e.target.checked })}
                />
                <Checkbox
                  id="unread"
                  name="unread"
                  label="Unread"
                  checked={form.unread}
                  onChange={(e) => updateForm({ unread: e.target.checked })}
                />
              </FieldGroup>
              <Field label="Date range" id="datepicker-range-start" inline>
                {({ id }) =>
                  dateRange(
                    { id, value: form.startDate },
                    {
                      id: "datepicker-range-end",
                      value: form.endDate,
                      label: "Date range end",
                    },
                    (v) => updateForm({ startDate: v }),
                    (v) => updateForm({ endDate: v })
                  )
                }
              </Field>
              <Field label="Query filter" inline>
                {(control) => (
                  <Input
                    {...control}
                    type="text"
                    readOnly
                    disabled
                    value={queryFilter}
                    className="font-mono text-xs"
                  />
                )}
              </Field>
            </>
          )}

          {!missingService && service === "photos" && (
            <PhotosPick account={account} />
          )}

          {!missingService && service === "drive" && (
            <>
              <FieldGroup label="Files">
                <Checkbox
                  id="ownedByMe"
                  label="Owned by me"
                  checked={form.ownedByMe}
                  disabled={form.editQuery}
                  onChange={(e) => updateForm({ ownedByMe: e.target.checked })}
                />
                <Checkbox
                  id="includeTrash"
                  label="Include trash"
                  checked={form.includeTrash}
                  disabled={form.editQuery}
                  onChange={(e) =>
                    updateForm({ includeTrash: e.target.checked })
                  }
                />
              </FieldGroup>

              <FieldGroup label="File types">
                {driveFileTypes.map(({ value, label }) => (
                  <Checkbox
                    key={value}
                    label={label}
                    checked={form.fileTypes.includes(value)}
                    disabled={form.editQuery}
                    onChange={(e) => toggleFileType(value, e.target.checked)}
                  />
                ))}
              </FieldGroup>

              <Field
                label="Modified"
                id="drive-modified-start"
                hint="Dates are in UTC."
                inline
              >
                {({ id }) =>
                  dateRange(
                    { id, value: form.modifiedFrom },
                    {
                      id: "drive-modified-end",
                      value: form.modifiedTo,
                      label: "Modified to",
                    },
                    (v) => updateForm({ modifiedFrom: v }),
                    (v) => updateForm({ modifiedTo: v }),
                    form.editQuery
                  )
                }
              </Field>

              <Field
                label="Folder"
                hint="Paste a folder link or ID; leave empty for the whole Drive."
                inline
              >
                {(control) => (
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-4">
                    <Input
                      {...control}
                      type="text"
                      placeholder="https://drive.google.com/drive/folders/…"
                      value={form.folder}
                      onChange={(e) => updateForm({ folder: e.target.value })}
                    />
                    {form.folder.trim() !== "" && (
                      <Checkbox
                        label="Include subfolders"
                        checked={form.recursive}
                        onChange={(e) =>
                          updateForm({ recursive: e.target.checked })
                        }
                        className="shrink-0"
                      />
                    )}
                  </div>
                )}
              </Field>

              <Field label="Query" inline>
                {(control) => (
                  <div className="grid gap-2">
                    <Input
                      {...control}
                      type="text"
                      disabled={!form.editQuery}
                      value={driveQuery}
                      onChange={(e) => updateForm({ rawQuery: e.target.value })}
                      className="font-mono text-xs"
                    />
                    <Checkbox
                      label="Edit query"
                      checked={form.editQuery}
                      onChange={(e) => toggleEditQuery(e.target.checked)}
                    />
                  </div>
                )}
              </Field>
            </>
          )}
        </div>

        {/* A Photos scan starts from its pick instead. */}
        {service !== "photos" && (
          <div className="mt-6 flex flex-col gap-3 border-t border-line pt-4 sm:flex-row sm:items-center">
            <Button
              loading={isPending}
              disabled={missingService}
              onClick={submitRequest}
              className="w-full sm:w-auto"
            >
              {isPending ? "Submitting…" : "Submit"}
            </Button>
            {message && (
              <p
                className={`flex items-start gap-2 text-sm ${message.kind === "error" ? "text-danger" : "text-success"}`}
              >
                <Icon
                  name={message.kind === "error" ? "warning" : "check"}
                  className="mt-0.5 shrink-0"
                />
                <span>
                  {message.text}
                  {message.scanId !== undefined && (
                    <>
                      {" "}
                      <Link
                        to="/scans/$scanId"
                        params={{ scanId: String(message.scanId) }}
                        search={{ page: 1 }}
                        className="font-medium text-accent hover:underline"
                      >
                        View results
                      </Link>
                    </>
                  )}
                </span>
              </p>
            )}
          </div>
        )}
      </Card>

      <ScanProgress />
    </div>
  );
}
