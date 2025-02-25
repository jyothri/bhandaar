import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/requests")({
  component: Requests,
});

function Requests() {
  return <div className="p-2">History of Requests!</div>;
}
