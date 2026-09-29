import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  cancelPhotosPick,
  getActivePhotosPick,
  getPhotosPick,
  startPhotosPick,
} from "../api";
import { queryKeys } from "../api/queryKeys";
import { Account } from "../types/accounts";
import { PhotosPick as Pick } from "../types/photos";
import Button from "./ui/Button";
import Icon from "./ui/Icon";
import Spinner from "./ui/Spinner";

// A Google Photos scan: the user picks items in Google Photos, and the
// backend scans what they picked. See docs/specs/photos-picker.md, "UI".

/** How often a pick's state is checked while it's under way. */
export const PICK_POLL_MS = 3000;

const underWay = (pick?: Pick | null) =>
  pick?.state === "waiting" || pick?.state === "scanning";

/** Where the user picks; the window closes itself once they're done. */
const pickerLink = (pick: Pick) => `${pick.pickerUri}/autoclose`;

const timeFormat = new Intl.DateTimeFormat(undefined, {
  hour: "numeric",
  minute: "2-digit",
});

export default function PhotosPick({ account }: { account?: Account }) {
  const queryClient = useQueryClient();
  // The pick this page started; else one already under way, from before a
  // reload or another tab.
  const [sessionKey, setSessionKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [popupBlocked, setPopupBlocked] = useState(false);

  const { data: active } = useQuery({
    queryKey: queryKeys.activePhotosPick,
    queryFn: getActivePhotosPick,
  });
  const key = sessionKey ?? active?.sessionKey ?? null;

  const { data: pick } = useQuery({
    queryKey: queryKeys.photosPick(key ?? ""),
    queryFn: () => getPhotosPick(key!),
    enabled: key !== null,
    refetchInterval: (query) =>
      underWay(query.state.data) ? PICK_POLL_MS : false,
  });

  // Once a pick is done, its scan is new in the history and in Browse.
  const state = pick?.state;
  useEffect(() => {
    if (state === "done") {
      queryClient.invalidateQueries({ queryKey: queryKeys.allScanRequests });
      queryClient.invalidateQueries({ queryKey: queryKeys.scannedAccounts });
      queryClient.invalidateQueries({ queryKey: queryKeys.browseSources });
      queryClient.invalidateQueries({ queryKey: ["accountPhotos"] });
    }
  }, [state, queryClient]);

  const start = useMutation({ mutationFn: startPhotosPick });
  const cancel = useMutation({
    mutationFn: cancelPhotosPick,
    onSuccess: (_, cancelled) =>
      queryClient.invalidateQueries({
        queryKey: queryKeys.photosPick(cancelled),
      }),
    onError: (e) => setError(`Failed to cancel: ${e.message}`),
  });

  function startPick() {
    if (!account) {
      setError("Please select an account.");
      return;
    }
    setError(null);
    setPopupBlocked(false);
    // Opened now, while the click counts, or the browser blocks it; it goes
    // to Google Photos once the backend has a session.
    const popup = window.open("", "_blank");
    start.mutate(account.clientKey, {
      onSuccess: (started) => {
        queryClient.setQueryData(
          queryKeys.photosPick(started.sessionKey),
          started
        );
        setSessionKey(started.sessionKey);
        if (popup) {
          popup.opener = null;
          popup.location.href = pickerLink(started);
        } else {
          setPopupBlocked(true);
        }
      },
      onError: (e) => {
        popup?.close();
        setError(`Failed to start picking: ${e.message}`);
      },
    });
  }

  return (
    <div className="grid gap-4">
      <div className="grid gap-2 text-sm text-muted">
        <p>
          Pick photos and videos in Google Photos, up to 2,000 at a time. The
          scan covers only what you pick; each scan is its own pick.
        </p>
        <p>
          Sizes are of the copy Google Photos keeps. For items uploaded at
          Storage saver quality, that can be far smaller than the original file,
          and items uploaded that way before June 2021 take no quota at all, so
          the total isn't your quota use.
        </p>
      </div>

      {pick && underWay(pick) ? (
        <PickUnderWay
          pick={pick}
          popupBlocked={popupBlocked}
          cancelling={cancel.isPending}
          onCancel={() => cancel.mutate(pick.sessionKey)}
        />
      ) : (
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
          <Button
            loading={start.isPending}
            onClick={startPick}
            className="w-full sm:w-auto"
          >
            Pick in Google Photos
          </Button>
          {pick && <PickOutcome pick={pick} />}
        </div>
      )}

      {error && (
        <p className="flex items-start gap-2 text-sm text-danger">
          <Icon name="warning" className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </p>
      )}
    </div>
  );
}

function PickUnderWay({
  pick,
  popupBlocked,
  cancelling,
  onCancel,
}: {
  pick: Pick;
  popupBlocked: boolean;
  cancelling: boolean;
  onCancel: () => void;
}) {
  if (pick.state === "scanning") {
    return (
      <div
        role="status"
        className="flex items-center gap-2 rounded-md bg-accent-soft px-4 py-3 text-sm"
      >
        <Spinner />
        <span>
          Picked. Scanning what you picked…
          {pick.scanId !== undefined && (
            <>
              {" "}
              <ResultsLink scanId={pick.scanId} />
            </>
          )}
        </span>
      </div>
    );
  }
  return (
    <div
      role="status"
      className="flex flex-col items-start gap-3 rounded-md bg-accent-soft px-4 py-3 text-sm sm:flex-row sm:items-center sm:justify-between"
    >
      <span className="flex items-center gap-2">
        <Spinner />
        <span>
          {popupBlocked
            ? "Your browser blocked the Google Photos window: open it to pick,"
            : "Waiting for you to pick in Google Photos,"}{" "}
          until {timeFormat.format(new Date(pick.pickBy))}.
        </span>
      </span>
      <span className="flex gap-2">
        <a
          href={pickerLink(pick)}
          target="_blank"
          rel="noopener noreferrer"
          className="font-medium text-accent hover:underline"
        >
          Open Google Photos
        </a>
        <Button
          variant="secondary"
          size="sm"
          loading={cancelling}
          onClick={onCancel}
        >
          Cancel
        </Button>
      </span>
    </div>
  );
}

function PickOutcome({ pick }: { pick: Pick }) {
  if (pick.state === "done" && pick.scanId !== undefined) {
    return (
      <p className="flex items-start gap-2 text-sm text-success">
        <Icon name="check" className="mt-0.5 shrink-0" />
        <span>
          Scan {pick.scanId} is done. <ResultsLink scanId={pick.scanId} />
        </span>
      </p>
    );
  }
  const text =
    pick.state === "expired"
      ? "Nothing was picked in time. Pick again when you're ready."
      : pick.state === "cancelled"
        ? "Pick cancelled."
        : null;
  return text && <p className="text-sm text-muted">{text}</p>;
}

function ResultsLink({ scanId }: { scanId: number }) {
  return (
    <Link
      to="/scans/$scanId"
      params={{ scanId: String(scanId) }}
      search={{ page: 1 }}
      className="font-medium text-accent hover:underline"
    >
      View results
    </Link>
  );
}
