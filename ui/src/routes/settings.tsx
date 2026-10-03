import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { saveSettings } from "../api";
import { queryKeys } from "../api/queryKeys";
import { useSettings } from "../components/hooks/useSettings";
import Card from "../components/ui/Card";
import Switch from "../components/ui/Switch";
import { Settings as SettingsData } from "../types/settings";

// Settings, from the user menu: display preferences, saved per user on the
// server, so they follow the user to any browser.

export const Route = createFileRoute("/settings")({
  component: Settings,
});

function Settings() {
  const queryClient = useQueryClient();
  const { data, error } = useSettings();
  // Each change shows at once and is saved in turn (one scope runs its
  // saves in order), so quick changes all land, the last one last. A
  // failed save puts back what the server has.
  const save = useMutation({
    mutationFn: saveSettings,
    scope: { id: "settings" },
    onMutate: async (next) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.settings });
      queryClient.setQueryData(queryKeys.settings, next);
    },
    onError: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.settings }),
  });

  if (error && !data) {
    return (
      <p className="py-6 text-sm text-danger">
        Couldn't load your settings: {error.message}
      </p>
    );
  }
  if (!data) {
    return <p className="py-6 text-sm text-muted">Loading…</p>;
  }
  const change = (changes: Partial<SettingsData>) =>
    save.mutate({ ...data, ...changes });

  return (
    <div className="space-y-4 pt-6">
      <h1 className="text-xl font-semibold">Settings</h1>
      <Card title="Display">
        <div className="space-y-1">
          <Switch
            checked={data.show_gcs}
            onChange={(on) => change({ show_gcs: on })}
            label="Show Google Cloud Storage"
          />
          <p className="text-sm text-muted">
            Adds a Google Cloud Storage tab to the Request and Browse pages, for
            scanning and browsing the buckets of your Google Cloud projects.
            Hiding it changes nothing else: buckets already scanned still show
            in Duplicates, Manage data and Request History.
          </p>
        </div>
        {save.error && (
          <p role="alert" className="mt-3 text-sm text-danger">
            Couldn't save: {save.error.message}
          </p>
        )}
      </Card>
    </div>
  );
}
