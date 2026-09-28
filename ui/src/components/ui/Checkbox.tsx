import { ComponentProps, ReactNode } from "react";

/** A native checkbox in the accent colour, with its label beside it. */
export default function Checkbox({
  label,
  className = "",
  ...props
}: Omit<ComponentProps<"input">, "type"> & { label: ReactNode }) {
  return (
    <label
      className={`inline-flex cursor-pointer items-center gap-2 text-sm has-disabled:cursor-not-allowed has-disabled:opacity-50 pointer-coarse:min-h-10 ${className}`}
    >
      <input
        type="checkbox"
        className="size-4 shrink-0 cursor-pointer rounded accent-accent-solid disabled:cursor-not-allowed"
        {...props}
      />
      {label}
    </label>
  );
}
