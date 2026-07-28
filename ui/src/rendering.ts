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

export function alertVisualClasses(
  severity: 'critical' | 'warning' | 'info',
  status: 'firing' | 'resolved',
): { card: string; severity: string; status: string } {
  const accent = status === 'resolved' ? 'resolved' : severity;
  return {
    card: `alert-card alert-card--${accent}`,
    severity: `severity-pill severity-pill--${accent}`,
    status: `alert-status alert-status--${status}`,
  };
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
