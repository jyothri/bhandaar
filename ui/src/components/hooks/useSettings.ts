import { useQuery } from "@tanstack/react-query";
import { getSettings } from "../../api";
import { queryKeys } from "../../api/queryKeys";

/**
 * The user's settings, shared by every page that reads them; the root
 * route loads them with the session. Refetched when the window regains
 * focus, so a change made in another tab or browser shows up.
 */
export const settingsQuery = {
  queryKey: queryKeys.settings,
  queryFn: getSettings,
  staleTime: 60_000,
  refetchOnWindowFocus: true,
};

export function useSettings() {
  return useQuery(settingsQuery);
}

/**
 * Whether the Google Cloud Storage tab is shown on the Request and Browse
 * pages: undefined until the settings have loaded, or when they can't be,
 * so a page asked for Cloud Storage stays on it rather than hiding data
 * the user may be using.
 */
export function useShowGcs(): boolean | undefined {
  return useSettings().data?.show_gcs;
}
