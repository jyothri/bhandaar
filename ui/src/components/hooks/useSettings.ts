import { useQuery } from "@tanstack/react-query";
import { getSettings } from "../../api";
import { queryKeys } from "../../api/queryKeys";

/** The user's settings, shared by every page that reads them. */
export function useSettings() {
  return useQuery({
    queryKey: queryKeys.settings,
    queryFn: getSettings,
    staleTime: Infinity,
  });
}

/**
 * Whether Google Cloud Storage is shown on the Request and Browse pages:
 * undefined until the settings have loaded, so a page asked for Cloud
 * Storage doesn't turn away from it before then.
 */
export function useGcsEnabled(): boolean | undefined {
  const { data, isError } = useSettings();
  return isError ? false : data?.gcs_enabled;
}
