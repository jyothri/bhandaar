import { useState } from "react";

import { setUnmasked, useMaskings, useUnmasked } from "../masking";
import Switch from "./ui/Switch";

/** A switch between masked (on) and unmasked (off), everywhere. */
export function MaskToggle() {
  const shown = useUnmasked();
  return (
    <Switch
      checked={!shown}
      onChange={(masked) => setUnmasked(!masked)}
      label="Mask"
    />
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
