import Badge from "./ui/Badge";
import Spinner from "./ui/Spinner";

/**
 * A scan's status as a badge. Scans still open after the server stopped
 * read "Failed" (interrupted); scans from before statuses were recorded
 * read "Completed".
 */
export default function ScanStatus({ status }: { status: string }) {
  if (status === "Running") {
    return (
      <Badge tone="warning">
        <Spinner size={12} />
        Running
      </Badge>
    );
  }
  if (status === "Failed") {
    return <Badge tone="danger">Failed</Badge>;
  }
  if (status === "Completed") {
    return <Badge tone="success">Completed</Badge>;
  }
  return <Badge>{status}</Badge>;
}
