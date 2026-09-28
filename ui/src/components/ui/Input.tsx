import { ComponentProps } from "react";

/** Text-like inputs: one height, border and focus ring. */
export default function Input({
  className = "",
  ...props
}: ComponentProps<"input">) {
  return (
    <input
      className={`h-9 w-full rounded-md border border-line bg-surface px-3 text-sm text-fg shadow-xs placeholder:text-muted focus:border-muted focus:outline-none focus:ring-3 focus:ring-accent/15 disabled:opacity-50 pointer-coarse:h-10 ${className}`}
      {...props}
    />
  );
}
