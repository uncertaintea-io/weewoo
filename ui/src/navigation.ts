export function searchValueForRender(
  previousRoute: string | undefined,
  currentRoute: string,
  currentSearchValue: string,
): string {
  return previousRoute === currentRoute ? currentSearchValue : '';
}
