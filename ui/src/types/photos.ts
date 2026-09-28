// Google Photos picks. See docs/specs/photos-picker.md.

export type PhotosPickState =
  | "waiting" // created; the user hasn't picked yet
  | "scanning" // picked; the scan is running
  | "done" // the scan finished, or failed
  | "expired" // not picked in time
  | "cancelled";

/** A Google Photos picking session. */
export type PhotosPick = {
  sessionKey: string;
  // Where the user picks, in Google Photos.
  pickerUri: string;
  state: PhotosPickState;
  // Once picked.
  scanId?: number;
  // When the user has to have picked by (ISO).
  pickBy: string;
};
