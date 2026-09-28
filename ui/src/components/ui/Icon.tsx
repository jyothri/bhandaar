import { ComponentProps } from "react";

// Inline SVG icons, drawn in currentColor on a 24-unit grid with 2-unit
// strokes. Decorative unless given a label.

const paths = {
  chevronDown: <path d="m6 9 6 6 6-6" />,
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
  close: <path d="M6 6l12 12M18 6 6 18" />,
  logOut: (
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" />
  ),
  warning: (
    <path d="M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
  ),
  user: <path d="M20 21a8 8 0 0 0-16 0M12 13a5 5 0 1 0 0-10 5 5 0 0 0 0 10z" />,
};

export type IconName = keyof typeof paths;

export default function Icon({
  name,
  size = 16,
  label,
  ...props
}: { name: IconName; size?: number; label?: string } & ComponentProps<"svg">) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      {...props}
    >
      {paths[name]}
    </svg>
  );
}

/** Bhandaar's mark, as in the favicon. */
export function Logo({ size = 24 }: { size?: number }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true">
      <rect width="32" height="32" rx="8" fill="#2563eb" />
      <rect x="8" y="8.5" width="10" height="3.5" rx="1.75" fill="#bfdbfe" />
      <rect x="8" y="14.25" width="13" height="3.5" rx="1.75" fill="#dbeafe" />
      <rect x="8" y="20" width="16" height="3.5" rx="1.75" fill="#ffffff" />
    </svg>
  );
}
