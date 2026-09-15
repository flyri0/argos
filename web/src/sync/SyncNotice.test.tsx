import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { act } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { dismissConflicts, recordConflicts, setSchemaMismatch } from "./status";
import { SyncNotice } from "./SyncNotice";

afterEach(() => {
  act(() => {
    setSchemaMismatch(false);
    dismissConflicts();
  });
});

describe("SyncNotice", () => {
  it("renders nothing while sync is healthy", () => {
    render(<SyncNotice />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows a persistent notice once a schema mismatch is detected", () => {
    render(<SyncNotice />);

    act(() => setSchemaMismatch(true));

    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("shows a conflict notice after a rollback and hides it when dismissed", async () => {
    const user = userEvent.setup();
    render(<SyncNotice />);

    act(() => recordConflicts(2));

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Some changes made on this device conflicted with changes from another device and were undone.",
    );

    await user.click(screen.getByRole("button", { name: "Dismiss" }));

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
