export function escapeHtml(value: string): string {
  const entityMap: Record<string, string> = {
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  };

  return value.replace(/[&<>"']/g, (character) => entityMap[character] ?? character);
}

export function renderServiceUrl(prometheusUrl: string): string {
  let parsedUrl: URL;

  try {
    parsedUrl = new URL(prometheusUrl);
  } catch {
    return `<span class="service-url">${escapeHtml(prometheusUrl)}</span>`;
  }

  if (parsedUrl.protocol !== 'http:' && parsedUrl.protocol !== 'https:') {
    return `<span class="service-url">${escapeHtml(prometheusUrl)}</span>`;
  }

  const safeUrl = escapeHtml(prometheusUrl);
  return `<a class="service-url" href="${safeUrl}" target="_blank" rel="noreferrer">${safeUrl}</a>`;
}

export function alertCardClasses(severity: string, status: string): string {
  const statusClass = status === 'resolved' ? ' alert-card--resolved' : '';
  return `alert-card alert-card--${severity}${statusClass}`;
}

export function groupAlertsByStatus<T extends { status: string }>(alerts: T[]): { active: T[]; resolved: T[] } {
  return {
    active: alerts.filter((alert) => alert.status === 'firing'),
    resolved: alerts.filter((alert) => alert.status === 'resolved'),
  };
}
