import { KeyboardEvent, ReactNode, useEffect, useId, useRef } from "react";

/**
 * A modal dialog over the page. It opens with initialFocus focused (else
 * its first button), keeps Tab inside it, closes on Escape or a click on
 * the backdrop, and hands focus back to what was focused before.
 */
export default function Dialog({
  title,
  children,
  actions,
  onClose,
  initialFocus,
}: {
  title: ReactNode;
  children: ReactNode;
  actions: ReactNode;
  onClose: () => void;
  initialFocus?: React.RefObject<HTMLElement | null>;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const titleId = useId();

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    (initialFocus?.current ?? panel.current?.querySelector("button"))?.focus();
    return () => opener?.focus();
    // Once, when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== "Tab" || !panel.current) {
      return;
    }
    const focusable = Array.from(
      panel.current.querySelectorAll<HTMLElement>(
        "button:not([disabled]), input:not([disabled]), a[href]"
      )
    );
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last?.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first?.focus();
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-black/40 p-4 sm:items-center"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        className="w-full max-w-lg rounded-lg border border-line bg-surface p-5 shadow-lg"
      >
        <h2 id={titleId} className="text-lg font-semibold">
          {title}
        </h2>
        <div className="mt-3 space-y-3 text-sm">{children}</div>
        <div className="mt-5 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          {actions}
        </div>
      </div>
    </div>
  );
}
