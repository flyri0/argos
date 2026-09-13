import type { AccountType } from "../../db";

// The fixed choice of values §7.1 accepts for accounts.type — any other
// value is rejected by the server with 400 INVALID_ACCOUNT_TYPE, so the
// client only ever offers these six rather than free text.
export const ACCOUNT_TYPES: AccountType[] = [
  "checking",
  "savings",
  "credit",
  "cash",
  "investment",
  "other",
];
