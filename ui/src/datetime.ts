export function datetimeLocalToUtcISOString(value: string): string {
  return new Date(`${value}Z`).toISOString();
}
