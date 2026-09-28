import { Fragment, ReactNode } from "react";

/**
 * The trail to the current page, under the nav tabs: each item but the
 * last is a link back up; the last is the page itself.
 */
export default function Breadcrumbs({ items }: { items: ReactNode[] }) {
  return (
    <nav aria-label="Breadcrumb" className="px-2 pt-2 text-sm">
      <ol className="flex flex-wrap items-center gap-1">
        {items.map((item, i) => (
          <Fragment key={i}>
            {i > 0 && (
              <li aria-hidden="true" className="text-gray-400">
                ›
              </li>
            )}
            <li
              aria-current={i === items.length - 1 ? "page" : undefined}
              className={i === items.length - 1 ? "font-semibold" : ""}
            >
              {item}
            </li>
          </Fragment>
        ))}
      </ol>
    </nav>
  );
}
