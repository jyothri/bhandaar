import { ComponentProps } from "react";
import Spinner from "./Spinner";
import { ButtonSize, ButtonVariant, buttonClasses } from "./styles";

// Buttons in four looks and two sizes. See docs/archive/ui-refresh.md,
// "Components". Links that look like buttons use buttonClasses.

export type ButtonProps = ComponentProps<"button"> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
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
      className={`${buttonClasses(variant, size)} ${className}`}
      {...props}
    >
      {loading && <Spinner />}
      {children}
    </button>
  );
}
