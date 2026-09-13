import { render, screen } from "@testing-library/react";
import { act } from "react";
import { afterEach, describe, expect, it } from "vitest";

import { setSchemaMismatch } from "./status";
import { SyncNotice } from "./SyncNotice";

afterEach(() => {
  act(() => setSchemaMismatch(false));
});

describe("SyncNotice", () => {
  it("renders nothing while sync is healthy", () => {
    render(<SyncNotice />);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("shows a persistent notice once a schema mismatch is detected", () => {
    render(<SyncNotice />);

    act(() => setSchemaMismatch(true));

    expect(screen.getByRole("status")).toBeInTheDocument();
  });
});
