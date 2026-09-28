import { ComponentProps } from "react";
import Spinner from "./Spinner";

// Buttons in four looks and two sizes. See docs/specs/ui-refresh.md,
// "Components".

const variants = {
  primary: "bg-accent-solid text-white hover:bg-accent-solid-hover shadow-sm",
  secondary:
    "border border-line bg-surface text-fg hover:bg-surface-muted shadow-sm",
  ghost: "text-fg hover:bg-surface-muted",
  danger: "bg-danger-solid text-white hover:opacity-90 shadow-sm",
};

const sizes = {
  sm: "h-8 px-3 text-sm gap-1.5",
  md: "h-9 px-4 text-sm gap-2 pointer-coarse:h-10",
};

export type ButtonProps = ComponentProps<"button"> & {
  variant?: keyof typeof variants;
  size?: keyof typeof sizes;
  // Shows a spinner and disables the button.
  loading?: boolean;
};

export default function Button({
  variant = "primary",
  size = "md",
  loading = false,
  disabled,
  className = "",
  children,
  type = "button",
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      className={`inline-flex items-center justify-center rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${sizes[size]} ${className}`}
      {...props}
    >
      {loading && <Spinner />}
      {children}
    </button>
  );
}
