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

export function collectionUptime(startedAt: string | undefined, now = new Date()): string {
  if (startedAt === undefined) return 'Unavailable';
  const elapsedMinutes = Math.max(0, Math.floor((now.getTime() - new Date(startedAt).getTime()) / 60_000));
  if (!Number.isFinite(elapsedMinutes)) return 'Unavailable';
  if (elapsedMinutes < 1) return '<1m';
  const days = Math.floor(elapsedMinutes / (24 * 60));
  const hours = Math.floor((elapsedMinutes % (24 * 60)) / 60);
  const minutes = elapsedMinutes % 60;
  if (days > 0) return `${String(days)}d ${String(hours)}h`;
  if (hours > 0) return `${String(hours)}h ${String(minutes)}m`;
  return `${String(minutes)}m`;
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

export interface AlertReviewTarget {
  id: number;
  revision: number;
}

interface AlertWithReviewOccurrences {
  serviceId?: number;
  serviceName: string;
  status: string;
  occurrences: {
    id: number;
    reviewRevision: number;
    chunkTimestamp?: string;
    reviewOverride?: boolean;
  }[];
}

export interface ServiceAlertReviewTargets {
  serviceKey: string;
  serviceName: string;
  targets: AlertReviewTarget[];
}

export function reviewableAnomalousOccurrencesByService(alerts: AlertWithReviewOccurrences[]): ServiceAlertReviewTargets[] {
  const grouped = new Map<string, ServiceAlertReviewTargets>();
  for (const alert of alerts) {
    if (alert.status !== 'firing') continue;
    const targets = alert.occurrences
      .filter((occurrence) => occurrence.chunkTimestamp !== undefined && occurrence.reviewOverride !== true)
      .map((occurrence) => ({ id: occurrence.id, revision: occurrence.reviewRevision }));
    if (targets.length === 0) continue;
    const serviceKey = alert.serviceId === undefined ? `name:${alert.serviceName}` : String(alert.serviceId);
    const group = grouped.get(serviceKey) ?? { serviceKey, serviceName: alert.serviceName, targets: [] };
    group.targets.push(...targets);
    grouped.set(serviceKey, group);
  }
  return Array.from(grouped.values());
}
