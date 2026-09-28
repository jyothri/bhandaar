import { useState } from "react";

import { setUnmasked, useMaskings, useUnmasked } from "../masking";

/** A switch between masked (on) and unmasked (off), everywhere. */
export function MaskToggle() {
  const shown = useUnmasked();
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-sm">
      <button
        type="button"
        role="switch"
        aria-checked={!shown}
        onClick={() => setUnmasked(!shown)}
        className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-600 ${
          shown ? "bg-gray-300 dark:bg-gray-600" : "bg-green-500"
        }`}
      >
        <span
          aria-hidden="true"
          className={`inline-block h-5 w-5 rounded-full bg-white shadow transition-transform ${
            shown ? "translate-x-0.5" : "translate-x-5.5"
          }`}
        />
      </button>
      Mask
    </label>
  );
}

/**
 * Text shown blurred while masked: hovering (on devices that can hover)
 * or keyboard focus shows it, and a click or tap toggles it, which is how
 * touch screens reveal it. Unmasked, it's plain text. It's only visual:
 * the text is in the page.
 */
export default function Masked({ text }: { text: string }) {
  const shown = useUnmasked();
  const maskedSince = useMaskings();
  // The masking this cell was revealed during, if any.
  const [revealedIn, setRevealedIn] = useState<number | null>(null);
  if (text === "") {
    return null;
  }
  if (shown) {
    return <span>{text}</span>;
  }
  const revealed = revealedIn === maskedSince;
  const toggle = () => setRevealedIn(revealed ? null : maskedSince);
  return (
    <span
      role="button"
      tabIndex={0}
      aria-pressed={revealed}
      onClick={toggle}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          toggle();
        }
      }}
      className={`cursor-pointer transition-[filter] ${
        revealed
          ? ""
          : "blur-sm select-none hover:blur-none hover:select-auto focus-visible:blur-none"
      }`}
    >
      {text}
    </span>
  );
}
