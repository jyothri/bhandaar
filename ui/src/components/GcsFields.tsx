import { useQuery } from "@tanstack/react-query";

import { getGcsBuckets, getGcsProjects } from "../api";
import { queryKeys } from "../api/queryKeys";
import { GcsForm } from "../gcsForm";
import { validProjectId } from "../gcsNames";
import { Account } from "../types/accounts";
import { GcsBucket } from "../types/gcs";
import Checkbox from "./ui/Checkbox";
import Field, { FieldGroup } from "./ui/Field";
import Input from "./ui/Input";
import Select from "./ui/Select";

// The Request page's Google Cloud Storage fields: a project, all its
// buckets or one, a prefix, and which object versions to include. See
// docs/specs/gcs-scans.md, "Request page".

/** A bucket as the bucket list shows it. */
function bucketLabel(b: GcsBucket): string {
  const parts = [b.name, b.location, b.storageClass];
  if (b.versioning) {
    parts.push("versioned");
  }
  if (b.requesterPays) {
    parts.push("Requester Pays: skipped");
  }
  return parts.filter(Boolean).join(" · ");
}

export default function GcsFields({
  account,
  form,
  onChange,
}: {
  account: Account;
  form: GcsForm;
  onChange: (changes: Partial<GcsForm>) => void;
}) {
  const clientKey = account.clientKey;
  const canList = account.canListProjects === true;
  const projects = useQuery({
    queryKey: queryKeys.gcsProjects(clientKey),
    queryFn: () => getGcsProjects(clientKey),
    enabled: canList,
    staleTime: Infinity,
  });
  const listed = canList && !projects.error && (projects.data?.length ?? 0) > 0;
  const projectOk = validProjectId(form.project);
  const buckets = useQuery({
    queryKey: queryKeys.gcsBuckets(clientKey, form.project),
    queryFn: () => getGcsBuckets(clientKey, form.project),
    enabled: projectOk,
    staleTime: 60_000,
  });

  const projectHint = !canList
    ? "This account didn't allow listing its projects: type a project ID."
    : projects.error
      ? `Couldn't list the projects (${projects.error.message}): type a project ID.`
      : projects.data?.length === 0
        ? "This account can see no projects: type a project ID."
        : undefined;
  const bucketHint = !projectOk
    ? "Pick a project first."
    : buckets.error
      ? `Couldn't list the buckets: ${buckets.error.message}`
      : buckets.isPending
        ? "Loading buckets…"
        : undefined;

  return (
    <>
      <Field label="Project" hint={projectHint} inline>
        {(control) =>
          listed ? (
            <Select
              {...control}
              value={form.project}
              onChange={(e) =>
                onChange({ project: e.target.value, bucket: "", prefix: "" })
              }
              className="w-full sm:w-80"
            >
              <option value="">Select one</option>
              {projects.data!.map((p) => (
                <option key={p.projectId} value={p.projectId}>
                  {p.displayName && p.displayName !== p.projectId
                    ? `${p.displayName} (${p.projectId})`
                    : p.projectId}
                </option>
              ))}
            </Select>
          ) : canList && projects.isPending ? (
            <p className="text-sm text-muted">Loading projects…</p>
          ) : (
            <Input
              {...control}
              type="text"
              placeholder="my-project-123456"
              value={form.project}
              onChange={(e) =>
                onChange({
                  project: e.target.value.trim(),
                  bucket: "",
                  prefix: "",
                })
              }
              className="font-mono text-xs sm:w-80"
            />
          )
        }
      </Field>

      <Field label="Buckets" hint={bucketHint} inline>
        {(control) => (
          <Select
            {...control}
            value={form.bucket}
            disabled={!projectOk || !buckets.data}
            onChange={(e) =>
              onChange({
                bucket: e.target.value,
                prefix: e.target.value ? form.prefix : "",
              })
            }
            className="w-full sm:w-auto"
          >
            <option value="">
              All buckets
              {buckets.data ? ` (${buckets.data.length})` : ""}
            </option>
            {buckets.data?.map((b) => (
              <option key={b.name} value={b.name}>
                {bucketLabel(b)}
              </option>
            ))}
          </Select>
        )}
      </Field>

      <Field
        label="Prefix"
        hint="Only objects whose names start with it, e.g. backups/2024/. Needs one bucket."
        inline
      >
        {(control) => (
          <Input
            {...control}
            type="text"
            disabled={form.bucket === ""}
            placeholder="backups/2024/"
            value={form.prefix}
            onChange={(e) => onChange({ prefix: e.target.value })}
            className="font-mono text-xs"
          />
        )}
      </Field>

      <FieldGroup label="Include">
        <Checkbox
          id="gcs-versions"
          label="Noncurrent versions"
          checked={form.versions}
          onChange={(e) => onChange({ versions: e.target.checked })}
        />
        <Checkbox
          id="gcs-soft-deleted"
          label="Soft-deleted objects"
          checked={form.softDeleted}
          onChange={(e) => onChange({ softDeleted: e.target.checked })}
        />
      </FieldGroup>
    </>
  );
}
