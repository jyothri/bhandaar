import { KeyboardEvent, ReactNode, useRef } from "react";

export type TabItem<T extends string> = {
  id: T;
  label: ReactNode;
  // A second line, e.g. totals.
  sub?: ReactNode;
  disabled?: boolean;
};

/**
 * A row of tabs: role="tablist", the selected tab focusable, and arrow
 * keys (Home, End) moving between the enabled ones. Scrolls sideways in
 * its own row when it doesn't fit.
 */
export default function Tabs<T extends string>({
  items,
  value,
  onChange,
  label,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (id: T) => void;
  label: string;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const enabled = items.flatMap((t, i) => (t.disabled ? [] : [i]));

  function onKeyDown(e: KeyboardEvent, index: number) {
    const at = enabled.indexOf(index);
    const to =
      e.key === "ArrowRight"
        ? enabled[(at + 1) % enabled.length]
        : e.key === "ArrowLeft"
          ? enabled[(at - 1 + enabled.length) % enabled.length]
          : e.key === "Home"
            ? enabled[0]
            : e.key === "End"
              ? enabled[enabled.length - 1]
              : undefined;
    if (to === undefined) {
      return;
    }
    e.preventDefault();
    refs.current[to]?.focus();
    onChange(items[to].id);
  }

  return (
    <div
      role="tablist"
      aria-label={label}
      className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1 sm:mx-0 sm:px-0"
    >
      {items.map((t, i) => {
        const selected = t.id === value;
        return (
          <button
            key={t.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            aria-selected={selected}
            disabled={t.disabled}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(t.id)}
            onKeyDown={(e) => onKeyDown(e, i)}
            className={`shrink-0 rounded-lg border px-4 py-2 text-left text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
              selected
                ? "border-accent bg-accent-soft"
                : "border-line bg-surface hover:bg-surface-muted"
            }`}
          >
            <span
              className={`block font-medium ${selected ? "text-accent" : ""}`}
            >
              {t.label}
            </span>
            {t.sub && (
              <span className="block text-xs text-muted tabular-nums">
                {t.sub}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
