import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ScanProgress from "../components/ScanProgress";
import { Progress } from "../types/scans";

// A fake EventSource the test can push events through.
let source: FakeEventSource;
class FakeEventSource {
  static readonly CLOSED = 2;
  readyState = 0;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners = new Map<string, (e: MessageEvent) => void>();
  constructor() {
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    source = this;
  }
  addEventListener(type: string, listener: (e: MessageEvent) => void) {
    this.listeners.set(type, listener);
  }
  close() {
    this.readyState = 2;
  }
  emit(progress: Partial<Progress>) {
    act(() =>
      this.listeners.get("progress")?.(
        new MessageEvent("progress", { data: JSON.stringify(progress) })
      )
    );
  }
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
});

const counts = {
  client_key: "k1",
  active_count: 2,
  completion_pct: 0,
  elapsed_in_sec: 65,
  eta_in_sec: 0,
};

const progressCell = () =>
  screen.getAllByRole("cell")[4] as HTMLTableCellElement;

describe("ScanProgress", () => {
  it("shows a running scan with an indeterminate bar", () => {
    render(<ScanProgress />);
    source.emit({
      ...counts,
      scan_id: 4,
      processed_count: 7,
      status: "Running",
    });

    expect(screen.getByLabelText("Scan in progress")).toBeInTheDocument();
    expect(screen.getByText("1:05")).toBeInTheDocument();
  });

  it("replaces the bar with Completed, keeping the counts", () => {
    render(<ScanProgress />);
    source.emit({
      ...counts,
      scan_id: 4,
      processed_count: 7,
      status: "Running",
    });
    source.emit({ scan_id: 4, status: "Completed" });

    expect(progressCell()).toHaveTextContent("Completed");
    expect(screen.queryByLabelText("Scan in progress")).toBeNull();
    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText("1:05")).toBeInTheDocument();
  });

  it("shows Failed with the error, even for a scan with no progress yet", () => {
    render(<ScanProgress />);
    source.emit({
      scan_id: 4,
      status: "Failed",
      error: 'auth: cannot fetch token: 400\n{"error": "invalid_grant"}',
    });

    expect(progressCell()).toHaveTextContent(
      "Failed: Google rejected this account's saved sign-in (invalid_grant). Link the account again."
    );
    expect(screen.queryByLabelText("Scan in progress")).toBeNull();
  });

  it("keeps the final status when a late Running event arrives", () => {
    render(<ScanProgress />);
    source.emit({ scan_id: 4, status: "Failed", error: "boom" });
    source.emit({
      ...counts,
      scan_id: 4,
      processed_count: 7,
      status: "Running",
    });

    expect(progressCell()).toHaveTextContent("Failed: boom");
  });
});
