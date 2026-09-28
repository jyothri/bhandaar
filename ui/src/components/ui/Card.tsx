import { ComponentProps, ReactNode } from "react";

/** A panel on the page: an optional title and actions, then its body. */
export default function Card({
  title,
  actions,
  flush = false,
  className = "",
  children,
  ...props
}: Omit<ComponentProps<"section">, "title"> & {
  title?: ReactNode;
  actions?: ReactNode;
  // No padding, for content that lays out its own rows.
  flush?: boolean;
}) {
  return (
    <section
      className={`rounded-lg border border-line bg-surface shadow-sm ${flush ? "" : "p-3 sm:p-4"} ${className}`}
      {...props}
    >
      {(title || actions) && (
        <header className="mb-3 flex flex-wrap items-center justify-between gap-2">
          {title && <h2 className="text-base font-semibold">{title}</h2>}
          {actions && (
            <div className="flex flex-wrap items-center gap-2">{actions}</div>
          )}
        </header>
      )}
      {children}
    </section>
  );
}
