// Centralized money formatting for integer-minor-unit amounts. The
// backend always stores integer minor units (cents) with a currency
// code and never sends floats. When currency is unknown/empty we render
// a currency-neutral minor-unit string instead of guessing a symbol.

export function formatMinorUnits(cents: number, currency?: string): string {
  if (!currency) {
    return `${cents} minor units`;
  }
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency,
      currencyDisplay: "narrowSymbol",
    }).format(cents / 100);
  } catch {
    return `${cents} ${currency}`;
  }
}

export function formatSignedMinorUnits(cents: number, currency?: string): string {
  const base = formatMinorUnits(Math.abs(cents), currency);
  return cents < 0 ? `-${base}` : base;
}
