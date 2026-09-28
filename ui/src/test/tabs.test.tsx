import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import Tabs from "../components/ui/Tabs";

function Example() {
  const [value, setValue] = useState<"a" | "b" | "c" | "d">("a");
  return (
    <Tabs
      label="Service"
      value={value}
      onChange={setValue}
      items={[
        { id: "a", label: "Drive", sub: "504 files" },
        { id: "b", label: "Gmail" },
        { id: "c", label: "Photos", disabled: true },
        { id: "d", label: "Other" },
      ]}
    />
  );
}

describe("Tabs", () => {
  it("moves between enabled tabs with the arrow keys, Home and End", async () => {
    const user = userEvent.setup();
    render(<Example />);
    const tab = (name: RegExp) => screen.getByRole("tab", { name });
    expect(tab(/Drive/)).toHaveAttribute("aria-selected", "true");
    // Only the selected tab is in the tab order.
    expect(tab(/Gmail/)).toHaveAttribute("tabindex", "-1");

    await user.tab();
    expect(tab(/Drive/)).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(tab(/Gmail/)).toHaveAttribute("aria-selected", "true");
    // Photos is disabled, so it's skipped.
    await user.keyboard("{ArrowRight}");
    expect(tab(/Other/)).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(tab(/Drive/)).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(tab(/Other/)).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{Home}");
    expect(tab(/Drive/)).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{End}");
    expect(tab(/Other/)).toHaveAttribute("aria-selected", "true");
  });

  it("shows each tab's second line", () => {
    render(<Example />);
    expect(screen.getByRole("tab", { name: /Drive/ })).toHaveTextContent(
      "Drive504 files"
    );
  });
});
