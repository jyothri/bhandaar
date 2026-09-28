import { ReactNode } from "react";
import Icon from "./Icon";

/**
 * Previous · Page n of m · Next. link renders a link to a page, with the
 * given content and class; pages out of range show disabled.
 */
export default function Pager({
  page,
  pages,
  link,
}: {
  page: number;
  pages: number;
  link: (page: number, children: ReactNode, className: string) => ReactNode;
}) {
  const item =
    "inline-flex h-8 items-center gap-1 rounded-md px-2 text-sm pointer-coarse:h-10";
  const step = (to: number, children: ReactNode) =>
    to >= 1 && to <= pages ? (
      link(to, children, `${item} hover:bg-surface-muted`)
    ) : (
      <span className={`${item} text-muted opacity-50`} aria-disabled="true">
        {children}
      </span>
    );
  return (
    <nav
      aria-label="Pages"
      className="flex items-center justify-between gap-2 pt-3 sm:justify-end"
    >
      {step(
        page - 1,
        <>
          <Icon name="chevronRight" className="rotate-180" />
          Previous
        </>
      )}
      <span className="text-sm text-muted tabular-nums">
        Page {page} of {pages}
      </span>
      {step(
        page + 1,
        <>
          Next
          <Icon name="chevronRight" />
        </>
      )}
    </nav>
  );
}
