// @vitest-environment node
// No DOM needed; skips starting jsdom for this file.

import { beforeEach, describe, expect, it, vi } from "vitest";
import { getAccounts, getScanRequests, requestScan } from ".";
import { ScanType } from "../types/scans";

// fetchJson is private; these tests exercise it through the exported calls.

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

const reply = (status: number, body: string, contentType: string) =>
  fetchMock.mockResolvedValueOnce(
    new Response(body, {
      status,
      statusText: status === 502 ? "Bad Gateway" : "",
      headers: { "Content-Type": contentType },
    })
  );

describe("fetchJson", () => {
  it("returns the parsed body and calls the configured backend", async () => {
    reply(
      200,
      '[{"clientKey":"k1","displayName":"alice"}]',
      "application/json"
    );

    await expect(getAccounts()).resolves.toEqual([
      { clientKey: "k1", displayName: "alice" },
    ]);
    expect(fetchMock).toHaveBeenCalledWith(
      "http://backend.test/api/accounts",
      undefined
    );
  });

  it("throws the plain-text message that http.Error sends", async () => {
    reply(500, "Failed to retrieve accounts\n", "text/plain; charset=utf-8");

    await expect(getAccounts()).rejects.toThrow("Failed to retrieve accounts");
  });

  it("throws error.message from a JSON error response", async () => {
    reply(
      413,
      '{"error":{"code":"PAYLOAD_TOO_LARGE","message":"Request body exceeds maximum allowed size"}}',
      "application/json"
    );

    await expect(getAccounts()).rejects.toThrow(
      "Request body exceeds maximum allowed size"
    );
  });

  it("accepts a JSON error given as a plain string", async () => {
    reply(400, '{"error":"bad request"}', "application/json");

    await expect(getAccounts()).rejects.toThrow("bad request");
  });

  it("falls back to the HTTP status for other bodies, e.g. a proxy's HTML", async () => {
    reply(502, "<html><body>Bad Gateway</body></html>", "text/html");

    await expect(getAccounts()).rejects.toThrow("502 Bad Gateway");
  });

  it("falls back to the HTTP status for malformed JSON errors", async () => {
    reply(500, "{not json", "application/json");

    await expect(getAccounts()).rejects.toThrow(/^500/);
  });

  it("never produces [object Object]", async () => {
    reply(500, '{"error":{"code":"X"}}', "application/json");

    await expect(getAccounts()).rejects.not.toThrow("[object Object]");
  });
});

describe("getScanRequests", () => {
  it("URL-encodes the account key", async () => {
    reply(200, "[]", "application/json");

    await getScanRequests("a/b c?#");
    expect(fetchMock).toHaveBeenCalledWith(
      "http://backend.test/api/scans/requests/a%2Fb%20c%3F%23",
      undefined
    );
  });
});

describe("requestScan", () => {
  it("POSTs the scan as JSON", async () => {
    reply(200, '{"scan_id":42}', "application/json");
    const scan = {
      ScanType: ScanType.GMail,
      GMailScan: {
        Filter: "is:unread",
        ClientKey: "k1",
        RefreshToken: "",
        Username: "alice",
      },
    };

    await expect(requestScan(scan)).resolves.toEqual({ scan_id: 42 });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://backend.test/api/scans");
    expect(init?.method).toBe("POST");
    expect(init?.headers).toMatchObject({ "Content-Type": "application/json" });
    expect(JSON.parse(init?.body as string)).toEqual(scan);
  });
});
