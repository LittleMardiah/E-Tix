/**
 * Shared display helpers for the customer screens (Epic C).
 *
 * The API returns money as a numeric(15,2) string, e.g. "4000000.00"
 * (see backend/internal/event/model.go and internal/booking/model.go), so the
 * value is parsed as a number before it is rendered.
 */
export function formatRupiah(value: string | number | undefined): string {
  if (value === undefined || value === null || value === '') return 'Rp 0';
  const numeric = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(numeric)) return 'Rp 0';
  return new Intl.NumberFormat('id-ID', {
    style: 'currency',
    currency: 'IDR',
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  }).format(numeric);
}

/** Splits a numeric(15,2) string into its integer part ("4000000.00" -> 4000000). */
export function parseAmount(value: string | undefined): number {
  if (!value) return 0;
  const numeric = Number(value);
  return Number.isFinite(numeric) ? numeric : 0;
}

/** Formats a seat position as a row label, e.g. 12 -> "B4" (12 seats per row). */
export function seatLabel(position: number, seatsPerRow = 12): string {
  const rowIndex = Math.floor((position - 1) / seatsPerRow);
  const row = String.fromCharCode(65 + (rowIndex % 26));
  const column = ((position - 1) % seatsPerRow) + 1;
  return `${row}${column}`;
}
