// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

export const LIVE_REFRESH_MILLISECONDS = 30_000;
export const LIVE_REFRESH_OFFSET_MILLISECONDS = 2_000;

export function liveRefreshDelay(route: string, nowMilliseconds = Date.now()): number | undefined {
  if (route === 'alerts' || route === 'services' || /^service\/\d+$/.test(route)) {
    const elapsedInInterval = (
      nowMilliseconds - LIVE_REFRESH_OFFSET_MILLISECONDS
    ) % LIVE_REFRESH_MILLISECONDS;
    return elapsedInInterval === 0
      ? LIVE_REFRESH_MILLISECONDS
      : (LIVE_REFRESH_MILLISECONDS - elapsedInInterval) % LIVE_REFRESH_MILLISECONDS;
  }
  return undefined;
}
