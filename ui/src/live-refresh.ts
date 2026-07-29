export const LIVE_REFRESH_MILLISECONDS = 2000;

export function liveRefreshDelay(route: string): number | undefined {
  if (route === 'alerts') return 1000;
  if (route === 'services' || /^service\/\d+$/.test(route)) return LIVE_REFRESH_MILLISECONDS;
  return undefined;
}
