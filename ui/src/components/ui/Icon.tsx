import { ComponentProps } from "react";

// Inline SVG icons, drawn in currentColor on a 24-unit grid with 2-unit
// strokes. Decorative unless given a label.

// A page with a folded corner, for the file kinds drawn on one.
const page = (
  <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5" />
);

const paths = {
  chevronRight: <path d="m9 6 6 6-6 6" />,
  externalLink: (
    <path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
  ),
  folder: (
    <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
  ),
  file: page,
  image: (
    <path d="M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zM21 15l-5-5L5 21M9 10a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z" />
  ),
  video: (
    <path d="M4 6h11a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1zM16 10l5-3v10l-5-3" />
  ),
  audio: (
    <path d="M9 18V5l11-2v13M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM20 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0z" />
  ),
  document: (
    <>
      {page}
      <path d="M9 13h6M9 17h6" />
    </>
  ),
  sheet: (
    <>
      {page}
      <path d="M8 12h8v6H8zM12 12v6M8 15h8" />
    </>
  ),
  slides: (
    <path d="M3 4h18M4 4v10a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1V4M12 15v5M8 20h8" />
  ),
  archive: (
    <path d="M3 4h18v4H3zM5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8M10 12h4" />
  ),
  code: <path d="m8 7-5 5 5 5M16 7l5 5-5 5" />,
  cloud: (
    <path d="M17.5 19a4.5 4.5 0 1 0-1.4-8.8A6 6 0 0 0 4.6 12 3.5 3.5 0 0 0 6 19z" />
  ),
  hardDrive: (
    <path d="M22 12H2M5.5 5h13l3.5 7v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6zM6 16h.01M10 16h.01" />
  ),
  mail: (
    <path d="M4 5h16a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zM3 7l9 6 9-6" />
  ),
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
