// Class names shared by components and by links styled like them.

const buttonVariants = {
  primary: "bg-accent-solid text-white hover:bg-accent-solid-hover shadow-sm",
  secondary:
    "border border-line bg-surface text-fg hover:bg-surface-muted shadow-sm",
  ghost: "text-fg hover:bg-surface-muted",
  danger: "bg-danger-solid text-white hover:opacity-90 shadow-sm",
  // A destructive action that opens a confirmation, not one that acts.
  dangerOutline:
    "border border-danger/40 bg-surface text-danger hover:bg-danger/10",
};

const buttonSizes = {
  sm: "h-8 px-3 text-sm gap-1.5",
  md: "h-9 px-4 text-sm gap-2 pointer-coarse:h-10",
};

export type ButtonVariant = keyof typeof buttonVariants;
export type ButtonSize = keyof typeof buttonSizes;

/** A button's classes, for a <button> or a link that looks like one. */
export function buttonClasses(
  variant: ButtonVariant = "primary",
  size: ButtonSize = "md"
): string {
  return `inline-flex items-center justify-center rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${buttonVariants[variant]} ${buttonSizes[size]}`;
}
