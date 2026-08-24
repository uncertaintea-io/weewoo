// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

export function datetimeLocalToUtcISOString(value: string): string {
  return new Date(`${value}Z`).toISOString();
}

export function historicalRangeToUtc(start: string, end: string): { start: string; end: string } {
  const startUtc = datetimeLocalToUtcISOString(start);
  const endUtc = datetimeLocalToUtcISOString(end);
  if (startUtc >= endUtc) {
    throw new Error('Import start must be before import end.');
  }
  return { start: startUtc, end: endUtc };
}
