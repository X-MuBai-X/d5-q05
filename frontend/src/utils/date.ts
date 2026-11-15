/** UTC 自然日工具：配额按 UTC 自然日归属，页面上的日期筛选一律使用 UTC。 */

export function todayUtcDate(): string {
  return new Date().toISOString().slice(0, 10);
}

export function shiftUtcDate(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

export function formatUtcTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString().replace('T', ' ').slice(0, 19);
}

export function formatShortTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString().slice(5, 16).replace('T', ' ');
}
