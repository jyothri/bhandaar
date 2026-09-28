import { ReactNode, useId } from "react";

/**
 * A labelled form control, with an optional hint and error under it,
 * linked by aria-describedby. The control is a render function given its
 * id and describedby. With inline, the label sits beside the control from
 * sm up; otherwise above it.
 */
export default function Field({
  label,
  hint,
  error,
  inline = false,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  inline?: boolean;
  children: (control: { id: string; "aria-describedby"?: string }) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy =
    [hint && hintId, error && errorId].filter(Boolean).join(" ") || undefined;
  return (
    <div
      className={
        inline
          ? "grid gap-1.5 sm:grid-cols-[10rem_1fr] sm:items-center sm:gap-4"
          : "grid gap-1.5"
      }
    >
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      <div className="grid gap-1">
        {children({ id, "aria-describedby": describedBy })}
        {hint && (
          <p id={hintId} className="text-xs text-muted">
            {hint}
          </p>
        )}
        {error && (
          <p id={errorId} className="text-xs text-danger">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}
