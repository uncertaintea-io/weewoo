// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

export function searchValueForRender(
  previousRoute: string | undefined,
  currentRoute: string,
  currentSearchValue: string,
): string {
  return previousRoute === currentRoute ? currentSearchValue : '';
}
