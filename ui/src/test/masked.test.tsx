import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import Masked, { MaskToggle } from "../components/Masked";
import { resetMasking } from "../masking";

beforeEach(() => act(resetMasking));

describe("Masked", () => {
  it("starts blurred, and a click or tap toggles it", async () => {
    const user = userEvent.setup();
    render(<Masked text="Alice <a@example.com>" />);
    const cell = screen.getByRole("button", { name: "Alice <a@example.com>" });
    expect(cell).toHaveClass("blur-sm");
    expect(cell).toHaveAttribute("aria-pressed", "false");

    await user.click(cell);
    expect(cell).not.toHaveClass("blur-sm");
    expect(cell).toHaveAttribute("aria-pressed", "true");

    await user.click(cell);
    expect(cell).toHaveClass("blur-sm");
  });

  it("toggles from the keyboard", async () => {
    const user = userEvent.setup();
    render(<Masked text="Quarterly report" />);
    await user.tab();
    await user.keyboard("{Enter}");
    expect(
      screen.getByRole("button", { name: "Quarterly report" })
    ).not.toHaveClass("blur-sm");
  });

  it("shows and hides every cell with the toggle", async () => {
    const user = userEvent.setup();
    render(
      <>
        <MaskToggle />
        <Masked text="Alice" />
        <Masked text="Hello" />
      </>
    );
    // On, masking, to start with.
    expect(screen.getByRole("switch", { name: "Mask" })).toBeChecked();
    // One cell revealed on its own first.
    await user.click(screen.getByRole("button", { name: "Alice" }));

    await user.click(screen.getByRole("switch", { name: "Mask" }));
    expect(screen.getByRole("switch", { name: "Mask" })).not.toBeChecked();
    // Plain text: no longer buttons, nothing blurred.
    expect(screen.queryByRole("button", { name: "Hello" })).toBeNull();
    expect(screen.getByText("Hello")).not.toHaveClass("blur-sm");

    await user.click(screen.getByRole("switch", { name: "Mask" }));
    // Masked again, including the cell revealed before.
    expect(screen.getByRole("button", { name: "Alice" })).toHaveClass(
      "blur-sm"
    );
    expect(screen.getByRole("button", { name: "Hello" })).toHaveClass(
      "blur-sm"
    );
  });

  it("applies on every page", async () => {
    const user = userEvent.setup();
    const first = render(<MaskToggle />);
    await user.click(screen.getByRole("switch", { name: "Mask" }));
    first.unmount();
    // Another page, rendered later, starts unmasked.
    render(<Masked text="Hello" />);
    expect(screen.queryByRole("button", { name: "Hello" })).toBeNull();
  });
});
