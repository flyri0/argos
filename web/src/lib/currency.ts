// Shared currency formatting for minor-unit amounts (§5.2: amounts are
// stored in minor currency units). Accounts always carry an ISO 4217 code,
// but real multi-currency support is out of MVP scope, so a code Intl
// doesn't recognize falls back to a plain number rather than crashing.
export function formatCurrency(minorUnits: number, currency = "USD"): string {
  const amount = minorUnits / 100;
  try {
    return amount.toLocaleString(undefined, { style: "currency", currency });
  } catch {
    return `${amount.toFixed(2)} ${currency}`;
  }
}
