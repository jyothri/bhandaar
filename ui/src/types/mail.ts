import { OptionalInt64, OptionalString } from "./optionals";

export type MessageMetadata = {
  message_metadata_id: number;
  ScanId: number;
  MessageId: OptionalString;
  ThreadId: OptionalString;
  LabelIds: OptionalString;
  From: OptionalString;
  To: OptionalString;
  Subject: OptionalString;
  Date: OptionalString;
  SizeEstimate: OptionalInt64;
};
