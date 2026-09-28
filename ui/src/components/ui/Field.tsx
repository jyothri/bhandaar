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
  id: givenId,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  inline?: boolean;
  // The control's id; one is made up when missing.
  id?: string;
  children: (control: { id: string; "aria-describedby"?: string }) => ReactNode;
}) {
  const madeId = useId();
  const id = givenId ?? madeId;
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

/** A labelled group of controls, such as checkboxes, laid out like Field. */
export function FieldGroup({
  label,
  children,
}: {
  label: ReactNode;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="grid gap-1.5 sm:grid-cols-[10rem_1fr] sm:items-center sm:gap-4">
      <span id={id} className="text-sm font-medium">
        {label}
      </span>
      <div
        role="group"
        aria-labelledby={id}
        className="flex flex-wrap gap-x-6 gap-y-1"
      >
        {children}
      </div>
    </div>
  );
}
