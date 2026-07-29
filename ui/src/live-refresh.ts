export const LIVE_REFRESH_MILLISECONDS = 30_000;

export function liveRefreshDelay(route: string): number | undefined {
  if (route === 'alerts' || route === 'services' || /^service\/\d+$/.test(route)) {
    return LIVE_REFRESH_MILLISECONDS;
  }
  return undefined;
}
