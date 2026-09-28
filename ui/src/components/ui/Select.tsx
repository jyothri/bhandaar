import { ComponentProps } from "react";
import Icon from "./Icon";

/** A native <select>, which phones handle best, in the inputs' style. */
export default function Select({
  className = "",
  ...props
}: ComponentProps<"select">) {
  return (
    <span className={`relative inline-flex ${className}`}>
      <select
        className="h-9 w-full appearance-none rounded-md border border-line bg-surface pr-9 pl-3 text-sm text-fg shadow-xs focus:border-muted focus:ring-3 focus:ring-accent/15 focus:outline-none pointer-coarse:h-10"
        {...props}
      />
      <Icon
        name="chevronDown"
        className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-muted"
      />
    </span>
  );
}
