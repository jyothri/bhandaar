// The Request page's Google Cloud Storage fields. See
// docs/archive/gcs-scans.md, "Request page".

export type GcsForm = {
  project: string;
  // "" for every bucket of the project.
  bucket: string;
  prefix: string;
  versions: boolean;
  softDeleted: boolean;
};

export const initialGcsForm: GcsForm = {
  project: "",
  bucket: "",
  prefix: "",
  versions: true,
  softDeleted: true,
};
