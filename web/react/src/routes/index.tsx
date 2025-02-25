import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  component: Data,
});

function Data() {
  return <div className="p-2">Data about account</div>;
}
