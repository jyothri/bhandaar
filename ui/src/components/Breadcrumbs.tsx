import { Fragment, ReactNode } from "react";

/**
 * The trail to the current page, under the nav tabs: each item but the
 * last is a link back up; the last is the page itself.
 */
export default function Breadcrumbs({ items }: { items: ReactNode[] }) {
  return (
    <nav
      aria-label="Breadcrumb"
      className="py-3 text-sm text-muted [&_a]:no-underline [&_a:hover]:text-fg [&_a:hover]:underline"
    >
      <ol className="flex flex-wrap items-center gap-1.5">
        {items.map((item, i) => (
          <Fragment key={i}>
            {i > 0 && (
              <li aria-hidden="true" className="text-muted/60">
                ›
              </li>
            )}
            <li
              aria-current={i === items.length - 1 ? "page" : undefined}
              className={
                i === items.length - 1 ? "font-medium text-fg" : "min-w-0"
              }
            >
              {item}
            </li>
          </Fragment>
        ))}
      </ol>
    </nav>
  );
}
