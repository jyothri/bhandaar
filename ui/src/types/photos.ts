// Google Photos picks. See docs/specs/photos-picker.md.

export type PhotosPickState =
  | "waiting" // created; the user hasn't picked yet
  | "scanning" // picked; the scan is running
  | "done" // the scan finished, or failed
  | "expired" // not picked in time
  | "cancelled";

/** How a picked item's size was found; "unavailable" when it wasn't. */
export type SizeSource = "head" | "download" | "unavailable";

/** A media item a Google Photos scan picked. */
export type PickedItem = {
  media_item_id: string;
  media_type: "PHOTO" | "VIDEO";
  mime_type: string;
  filename: string;
  // When it was taken.
  create_time: string | null;
  width: number | null;
  height: number | null;
  camera_make: string;
  camera_model: string;
  focal_length: number | null;
  f_number: number | null;
  iso: number | null;
  exposure_time: string;
  fps: number | null;
  // Bytes of the copy Google Photos keeps; null when unavailable.
  size: number | null;
  size_source: SizeSource;
  // Only when it was downloaded to size it.
  md5: string;
};

/** A page of a Photos scan's picked items. */
export type PickedItemPage = {
  items: PickedItem[];
  page: number;
  page_size: number;
  total: number;
};

/** An item in an account's Photos, with the scan that last picked it. */
export type AccountPhoto = PickedItem & { scan_id: number };

export type AccountPhotoPage = {
  items: AccountPhoto[];
  total: number;
  page: number;
  page_size: number;
};

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
