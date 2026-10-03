import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { saveSettings } from "../api";
import { queryKeys } from "../api/queryKeys";
import { useSettings } from "../components/hooks/useSettings";
import Card from "../components/ui/Card";
import Switch from "../components/ui/Switch";
import { Settings as SettingsData } from "../types/settings";

// Settings, from the user menu: what the app shows. Saved per user on the
// server, so they follow the user to any browser.

export const Route = createFileRoute("/settings")({
  component: Settings,
});

function Settings() {
  const queryClient = useQueryClient();
  const { data, error } = useSettings();
  const save = useMutation({
    mutationFn: saveSettings,
    onSuccess: (saved) => queryClient.setQueryData(queryKeys.settings, saved),
  });

  if (error) {
    return (
      <p className="py-6 text-sm text-danger">
        Couldn't load your settings: {error.message}
      </p>
    );
  }
  if (!data) {
    return <p className="py-6 text-sm text-muted">Loading…</p>;
  }
  // What the switches show: the change being saved, else what's saved.
  const shown: SettingsData = save.isPending ? save.variables : data;
  const change = (changes: Partial<SettingsData>) => {
    if (!save.isPending) {
      save.mutate({ ...data, ...changes });
    }
  };

  return (
    <div className="space-y-4 pt-6">
      <h1 className="text-xl font-semibold">Settings</h1>
      <Card title="Sources">
        <div className="space-y-1">
          <Switch
            checked={shown.gcs_enabled}
            onChange={(on) => change({ gcs_enabled: on })}
            label="Google Cloud Storage"
          />
          <p className="text-sm text-muted">
            Scan and browse the buckets of your Google Cloud projects. When it's
            off, the Request and Browse pages don't show Cloud Storage; anything
            already scanned is kept.
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
