import './index.scss'
import { CancelImport, CreateService, DeleteService, GetService, GetServiceDetail, ListAlerts, ListAllServices, ResetServiceBaseline, ReviewAlertOccurrence, ServicesApiError, SetServicePaused, TestService, UpdateService, type AlertOccurrence, type AlertRecord, type CreateServiceInput, type Service, type ServiceChange } from './api';
import { historicalRangeToUtc } from './datetime';
import { liveRefreshDelay } from './live-refresh';
import { searchValueForRender } from './navigation';
import { alertCardClasses, escapeHtml, groupAlertsByStatus, renderServiceUrl, reviewableAnomalousOccurrencesByService, type AlertReviewTarget } from './rendering';

const app = document.querySelector<HTMLDivElement>('#app');
let liveRefreshTimer: number | undefined;
let lastRenderedRoute: string | undefined;
type Theme = 'light' | 'dark' | 'system';

function savedTheme(): Theme {
  const theme = localStorage.getItem('weewoo-theme');
  return theme === 'light' || theme === 'dark' ? theme : 'system';
}

function applyTheme(theme: Theme): void {
  if (theme === 'system') {
    document.documentElement.removeAttribute('data-theme');
  } else {
    document.documentElement.dataset.theme = theme;
  }
}

applyTheme(savedTheme());

function formatInterval(seconds: number): string {
  if (seconds < 60) {
    return `${String(seconds)}s`;
  }

  if (seconds < 3600) {
    return `${String(Math.round(seconds / 60))}m`;
  }

  return `${String(Math.round(seconds / 3600))}h`;
}

function serviceInitial(service: Service): string {
  return service.name.trim().charAt(0).toUpperCase() || 'S';
}

function statusLabel(state: Service['tracking']['state']): string {
  const label = state.replaceAll('_', ' ');
  return label.charAt(0).toUpperCase() + label.slice(1);
}

function formatTimestamp(value?: string): string {
  if (value === undefined) return 'Not yet';
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

interface PageMeta {
  eyebrow?: string;
  title?: string;
  description?: string;
  endpoint?: string;
}

function renderShell(content: string, apiResponse = 'Ready', page: PageMeta = {}): void {
  if (app === null) {
    return;
  }
  const route = currentRoute();
  const searchValue = searchValueForRender(
    lastRenderedRoute,
    route,
    document.querySelector<HTMLInputElement>('.search-box input')?.value ?? '',
  );
  lastRenderedRoute = route;

  app.innerHTML = `
    <div class="app-frame">
      <aside class="sidebar" aria-label="Primary navigation">
        <div class="sidebar-brand">
          <img class="brand-logo" src="/img/logo.svg" alt="" aria-hidden="true" />
          <div>
            <strong>WeeWoo Services</strong>
            <span>Monitoring console</span>
          </div>
        </div>
        <nav class="sidebar-nav">
          <a class="${window.location.hash === '#services' || window.location.hash === '' || window.location.hash.startsWith('#service') ? 'is-active' : ''}" href="#services">Services</a>
          <a class="${window.location.hash === '#alerts' ? 'is-active' : ''}" href="#alerts">Alerts</a>
          <a href="#incidents">Incidents</a>
          <a href="#integrations">Integrations</a>
          <a class="${window.location.hash === '#settings' ? 'is-active' : ''}" href="#settings">Settings</a>
        </nav>
        <article class="system-card">
          <span>System Status</span>
          <strong>Configuration API</strong>
          <p>Endpoint monitored through WeeWoo Services.</p>
        </article>
      </aside>
      <div class="workspace">
        <header class="top-bar">
          <label class="search-box">
            <span aria-hidden="true"></span>
            <input type="search" placeholder="Search services" />
          </label>
          <div class="top-actions">
            <div class="last-updated">
              <span class="status-dot status-dot--ok" aria-hidden="true"></span>
              <span id="service-count">0 services monitored</span>
              <small>Last updated: just now</small>
            </div>
            <a class="icon-button" href="#settings" aria-label="Settings">
              <span aria-hidden="true"></span>
            </a>
            <a class="avatar" href="#profile" aria-label="User profile"></a>
          </div>
        </header>
        <main class="page-shell">
          <section class="page-header">
            <div>
              <p class="eyebrow">${escapeHtml(page.eyebrow ?? 'WeeWoo Services')}</p>
              <h1>${escapeHtml(page.title ?? 'Service health dashboard')}</h1>
              <p>${escapeHtml(page.description ?? 'Live from the WeeWoo server configuration API.')}</p>
            </div>
            <article class="api-status-card">
              <div>
                <span>API Endpoint</span>
                <strong>${escapeHtml(page.endpoint ?? '/api/services')}</strong>
              </div>
              <div>
                <span>Response</span>
                <strong class="api-response${apiResponse === '200 OK' || apiResponse === 'Ready' ? ' is-success' : ''}">${escapeHtml(apiResponse)}</strong>
              </div>
            </article>
          </section>
          ${content}
        </main>
      </div>
    </div>
  `;
  bindShellInteractions();
  const search = document.querySelector<HTMLInputElement>('.search-box input');
  if (search !== null && searchValue !== '') {
    search.value = searchValue;
    search.dispatchEvent(new Event('input'));
  }
}

function bindShellInteractions(): void {
  const search = document.querySelector<HTMLInputElement>('.search-box input');
  search?.addEventListener('input', () => {
    const term = search.value.trim().toLowerCase();
    document.querySelectorAll<HTMLElement>('.service-dashboard, .alert-card').forEach((row) => {
      row.hidden = term !== '' && !(row.dataset.serviceName ?? '').includes(term);
    });
  });
}

function alertmanagerLabel(state: AlertRecord['alertmanagerState']): string {
  switch (state) {
    case 'accepted': return 'Accepted by Alertmanager';
    case 'failed': return 'Alertmanager handoff failed';
    case 'missed': return 'Alertmanager handoff missed';
    default: return 'Alertmanager handoff pending';
  }
}

function reviewLabel(occurrence: AlertOccurrence): string {
  if (occurrence.reviewOverride === true) return 'Accepted as normal';
  if (occurrence.reviewOverride === false) return 'Automated Verdict restored';
  return 'No manual override';
}

function renderEvidence(evidence: Record<string, unknown>): string {
  const entries = Object.entries(evidence);
  if (entries.length === 0) return '';
  return `<dl class="evidence-grid">${entries.map(([key, value]) => `
    <div><dt>${escapeHtml(key)}</dt><dd>${escapeHtml(String(value))}</dd></div>
  `).join('')}</dl>`;
}

function renderOccurrence(occurrence: AlertOccurrence): string {
  const reviewable = occurrence.chunkTimestamp !== undefined;
  const accepted = occurrence.reviewOverride === true;
  return `
    <article class="occurrence-row">
      <header>
        <div><strong>${escapeHtml(occurrence.summary)}</strong><time>${escapeHtml(formatTimestamp(occurrence.occurredAt))}</time></div>
        <span class="review-state${accepted ? ' is-accepted' : ''}">${escapeHtml(reviewLabel(occurrence))}</span>
      </header>
      ${renderEvidence(occurrence.evidence)}
      ${occurrence.technicalDetails === '' ? '' : `<details><summary>Technical details</summary><pre>${escapeHtml(occurrence.technicalDetails)}</pre></details>`}
      ${occurrence.reviewReason === undefined || occurrence.reviewReason === '' ? '' : `<p class="review-reason">Review note: ${escapeHtml(occurrence.reviewReason)}</p>`}
      ${reviewable ? `
        <div class="occurrence-actions">
          <button class="${accepted ? 'secondary-button' : 'primary-button'} review-occurrence" type="button"
            data-occurrence-id="${String(occurrence.id)}"
            data-review-revision="${String(occurrence.reviewRevision)}"
            data-review-accepted="${String(!accepted)}">
            ${accepted ? 'Restore automated Verdict' : 'Accept as normal'}
          </button>
        </div>
      ` : ''}
    </article>
  `;
}

function renderAlertCard(alert: AlertRecord): string {
  return `
    <article class="${escapeHtml(alertCardClasses(alert.severity, alert.status))}" data-alert-id="${String(alert.id)}" data-service-name="${escapeHtml(`${alert.serviceName} ${alert.title}`.toLowerCase())}">
      <header class="alert-card-header">
        <div>
          <div class="alert-labels">
            <span class="severity-pill severity-pill--${escapeHtml(alert.severity)}">${escapeHtml(alert.severity)}</span>
            <span class="alert-status">${escapeHtml(alert.status)}</span>
          </div>
          <h2>${escapeHtml(alert.title)}</h2>
          <p>${escapeHtml(alert.serviceName)} · Last observed ${escapeHtml(formatTimestamp(alert.lastOccurredAt))}</p>
        </div>
        <strong class="occurrence-total">${String(alert.occurrenceCount)}<small>occurrence${alert.occurrenceCount === 1 ? '' : 's'}</small></strong>
      </header>
      <p class="alert-description">${escapeHtml(alert.description)}</p>
      <div class="alert-guidance">
        <div><span>Impact</span><p>${escapeHtml(alert.impact)}</p></div>
        <div><span>Suggested action</span><p>${escapeHtml(alert.suggestedAction)}</p></div>
      </div>
      <div class="delivery-state delivery-state--${escapeHtml(alert.alertmanagerState)}">
        ${escapeHtml(alertmanagerLabel(alert.alertmanagerState))}
        ${alert.alertmanagerError === undefined ? '' : `<small>${escapeHtml(alert.alertmanagerError)}</small>`}
      </div>
      ${alert.resolutionReason === undefined || alert.resolutionReason === '' ? '' : `<p class="resolution-copy">Resolved: ${escapeHtml(alert.resolutionReason.replaceAll('_', ' '))}</p>`}
      <details class="occurrence-disclosure">
        <summary>View ${String(alert.occurrences.length)} occurrence${alert.occurrences.length === 1 ? '' : 's'} and evidence</summary>
        <div class="occurrence-list">${alert.occurrences.map(renderOccurrence).join('')}</div>
      </details>
    </article>
  `;
}

function renderAlerts(alerts: AlertRecord[]): void {
  const resolvedHistoryOpen = document.querySelector<HTMLDetailsElement>('.resolved-alerts-group')?.open ?? false;
  const expandedAlertIds = new Set(Array.from(
    document.querySelectorAll<HTMLDetailsElement>('.alert-card > .occurrence-disclosure[open]'),
  ).map((details) => details.closest<HTMLElement>('.alert-card')?.dataset.alertId).filter((id) => id !== undefined));
  const { active, resolved } = groupAlertsByStatus(alerts);
  const critical = active.filter((alert) => alert.severity === 'critical').length;
  const warning = active.filter((alert) => alert.severity === 'warning').length;
  const reviewGroups = reviewableAnomalousOccurrencesByService(active);
  renderShell(`
    <section class="summary-grid" aria-label="Alert summary">
      ${renderSummaryCard('Active alerts', active.length, 'unavailable')}
      ${renderSummaryCard('Critical', critical, 'unavailable')}
      ${renderSummaryCard('Warnings', warning, 'degraded')}
      ${renderSummaryCard('History', alerts.length - active.length, 'total')}
    </section>
    <section class="alert-panel">
      <div class="panel-header">
        <div><h2>Active alerts</h2><span>Conditions requiring attention</span></div>
        ${reviewGroups.length === 0 ? '' : `<div class="bulk-review-actions">${reviewGroups.map((group, index) => `
          <button class="secondary-button accept-service-anomalies" type="button" data-review-group-index="${String(index)}">
            Accept ${String(group.targets.length)} for ${escapeHtml(group.serviceName)}
          </button>
        `).join('')}</div>`}
      </div>
      ${active.length === 0 ? '<div class="empty-state alert-empty-state"><h2>No active alerts</h2><p>There are currently no conditions requiring attention.</p></div>' : `<div class="alert-list">${active.map(renderAlertCard).join('')}</div>`}
      ${resolved.length === 0 ? '' : `
        <details class="resolved-alerts-group">
          <summary><span>Resolved alerts</span><strong>${String(resolved.length)}</strong><small>Retained for 90 days</small></summary>
          <div class="alert-list">${resolved.map(renderAlertCard).join('')}</div>
        </details>
      `}
    </section>
  `, '200 OK', {
    eyebrow: 'WeeWoo Alerts',
    title: 'Alerts and anomaly history',
    description: 'User-visible conditions, evidence, recovery, and optional Bad-chunk review.',
    endpoint: '/api/alerts',
  });
  document.querySelector('#service-count')?.replaceChildren(`${String(active.length)} active alert${active.length === 1 ? '' : 's'}`);
  const resolvedHistory = document.querySelector<HTMLDetailsElement>('.resolved-alerts-group');
  if (resolvedHistory !== null) resolvedHistory.open = resolvedHistoryOpen;
  document.querySelectorAll<HTMLDetailsElement>('.alert-card > .occurrence-disclosure').forEach((details) => {
    const alertId = details.closest<HTMLElement>('.alert-card')?.dataset.alertId;
    details.open = alertId !== undefined && expandedAlertIds.has(alertId);
  });
  document.querySelectorAll<HTMLButtonElement>('.review-occurrence').forEach((button) => {
    button.addEventListener('click', () => { void reviewOccurrence(button); });
  });
  document.querySelectorAll<HTMLButtonElement>('.accept-service-anomalies').forEach((button) => {
    button.addEventListener('click', () => {
      const group = reviewGroups[Number(button.dataset.reviewGroupIndex)];
      void acceptServiceAnomalies(button, group.serviceName, group.targets);
    });
  });
}

async function acceptServiceAnomalies(button: HTMLButtonElement, serviceName: string, targets: AlertReviewTarget[]): Promise<void> {
  const count = targets.length;
  if (count === 0) return;
  if (!window.confirm(`Accept ${String(count)} Bad chunks for ${serviceName} as normal and make them eligible for future ECDF builds?`)) return;
  const reason = window.prompt(`Optional reason applied to every accepted ${serviceName} chunk:`, '');
  if (reason === null) return;
  button.disabled = true;
  button.textContent = `Accepting 0 of ${String(count)}…`;
  try {
    for (const [index, target] of targets.entries()) {
      await ReviewAlertOccurrence(target.id, target.revision, true, reason);
      button.textContent = `Accepting ${String(index + 1)} of ${String(count)}…`;
    }
    await loadAlerts();
  } catch (error) {
    await loadAlerts(false);
    window.alert(error instanceof Error ? error.message : 'Unable to accept every anomalous occurrence.');
  }
}

async function reviewOccurrence(button: HTMLButtonElement): Promise<void> {
  const accepted = button.dataset.reviewAccepted === 'true';
  if (accepted && !window.confirm('Accept this Bad chunk as normal and make it eligible for future ECDF builds?')) return;
  const reason = window.prompt(accepted ? 'Optional reason for accepting this chunk:' : 'Optional reason for restoring the automated Verdict:', '');
  if (reason === null) return;
  button.disabled = true;
  try {
    await ReviewAlertOccurrence(
      Number(button.dataset.occurrenceId),
      Number(button.dataset.reviewRevision),
      accepted,
      reason,
    );
    await loadAlerts();
  } catch (error) {
    window.alert(error instanceof Error ? error.message : 'Unable to review this occurrence.');
    button.disabled = false;
  }
}

async function loadAlerts(showLoading = true): Promise<void> {
  if (showLoading) {
    renderShell('<section class="alert-panel" aria-busy="true"><div class="skeleton-list"><div class="skeleton-row"></div><div class="skeleton-row"></div></div></section>', 'Loading', {
      eyebrow: 'WeeWoo Alerts', title: 'Alerts and anomaly history', description: 'Loading durable alert history.', endpoint: '/api/alerts',
    });
  }
  try {
    const alerts = await ListAlerts(true);
    if (currentRoute() === 'alerts') renderAlerts(alerts);
  } catch (error) {
    if (!showLoading || currentRoute() !== 'alerts') return;
    const response = apiResponseForError(error);
    renderShell(`<section class="error-panel"><strong class="error-code">${escapeHtml(response)}</strong><h2>Unable to load alerts</h2><p>Check the alert history database and retry.</p><button id="retry-alerts" class="retry-button" type="button">Retry</button></section>`, response, {
      eyebrow: 'WeeWoo Alerts', title: 'Alerts and anomaly history', description: 'Durable alert history is unavailable.', endpoint: '/api/alerts',
    });
    document.querySelector('#retry-alerts')?.addEventListener('click', () => { void loadAlerts(); });
  }
}

function renderMetricBox(label: string, value: string, detail: string, modifier = ''): string {
  const modifierClass = modifier === '' ? '' : ` metric-card--${modifier}`;

  return `
    <div class="metric-card${modifierClass}">
      <dt>${escapeHtml(label)}</dt>
      <dd>${escapeHtml(value)}</dd>
      <p>${escapeHtml(detail)}</p>
    </div>
  `;
}

function renderSummaryCard(label: string, value: number, modifier: string): string {
  return `
    <article class="summary-card summary-card--${modifier}">
      <span>${escapeHtml(label)}</span>
      <strong>${String(value)}</strong>
    </article>
  `;
}

function renderQueryBox(label: string, query: string): string {
  return `
    <div class="query-card">
      <dt>${escapeHtml(label)}</dt>
      <dd>${escapeHtml(query)}</dd>
    </div>
  `;
}

function renderLoading(): void {
  renderShell(`
    <section class="summary-grid" aria-label="Service summary">
      ${renderSummaryCard('Total Services', 0, 'total')}
      ${renderSummaryCard('Healthy', 0, 'healthy')}
      ${renderSummaryCard('Degraded', 0, 'degraded')}
      ${renderSummaryCard('Unavailable', 0, 'unavailable')}
    </section>
    <section class="service-panel" aria-busy="true">
      <div class="panel-header">
        <h2>Loading services</h2>
      </div>
      <div class="skeleton-list">
        <div class="skeleton-row"></div>
        <div class="skeleton-row"></div>
        <div class="skeleton-row"></div>
      </div>
    </section>
  `, 'Loading');
}

function renderEmpty(): void {
  renderShell(`
    <section class="summary-grid" aria-label="Service summary">
      ${renderSummaryCard('Total Services', 0, 'total')}
      ${renderSummaryCard('Healthy', 0, 'healthy')}
      ${renderSummaryCard('Degraded', 0, 'degraded')}
      ${renderSummaryCard('Unavailable', 0, 'unavailable')}
    </section>
    <section class="service-panel">
      <div class="empty-state">
        <h2>No services configured</h2>
        <p>Add services to the configuration database and they will appear here.</p>
        <a class="primary-button" href="#add">+ Add service</a>
      </div>
    </section>
  `, '200 OK');
  setServiceCount(0);
}

function apiResponseForError(error: unknown): string {
  if (error instanceof ServicesApiError) {
    return `${String(error.status)}${error.statusText === '' ? '' : ` ${error.statusText}`}`;
  }

  return 'Request Failed';
}

function renderError(error: unknown): void {
  const apiResponse = apiResponseForError(error);
  renderShell(`
    <section class="summary-grid" aria-label="Service summary">
      ${renderSummaryCard('Total Services', 0, 'total')}
      ${renderSummaryCard('Healthy', 0, 'healthy')}
      ${renderSummaryCard('Degraded', 0, 'degraded')}
      ${renderSummaryCard('Unavailable', 0, 'unavailable')}
    </section>
    <section class="error-panel" aria-live="polite">
      <strong class="error-code">${escapeHtml(apiResponse)}</strong>
      <span class="error-badge">${escapeHtml(apiResponse)}</span>
      <h2>Unable to load services</h2>
      <p>The API returned ${escapeHtml(apiResponse)}. Check that the service configuration endpoint exists and is routed correctly.</p>
      <button id="retry-services" class="retry-button" type="button">Retry</button>
    </section>
  `, apiResponse);
  document.querySelector('#retry-services')?.addEventListener('click', () => {
    void boot();
  });
}

function setServiceCount(count: number): void {
  const label = count === 1 ? '1 service monitored' : `${String(count)} services monitored`;
  document.querySelector('#service-count')?.replaceChildren(label);
}

function renderServices(services: Service[]): void {
  if (services.length === 0) {
    renderEmpty();
    return;
  }

  const healthy = services.filter((service) => service.tracking.state === 'healthy').length;
  const degraded = services.filter((service) => service.tracking.state === 'degraded').length;
  const unavailable = services.filter((service) => service.tracking.state === 'unavailable').length;
  const serviceRows = services.map((service) => `
    <article class="service-dashboard" data-service-name="${escapeHtml(service.name.toLowerCase())}">
      <header class="service-header">
        <div class="service-identity">
          <div class="service-avatar" aria-hidden="true">${escapeHtml(serviceInitial(service))}</div>
          <div>
            <div class="service-title">
              <h2><a href="#service/${String(service.id)}">${escapeHtml(service.name)}</a></h2>
              <span class="service-id">#${String(service.id)}</span>
            </div>
            ${renderServiceUrl(service.prometheusUrl)}
          </div>
        </div>
        <span class="status-pill status-pill--${escapeHtml(service.tracking.state)}">${escapeHtml(statusLabel(service.tracking.state))}</span>
      </header>

      <dl class="metric-grid">
        ${renderMetricBox('Current status', statusLabel(service.tracking.state), service.tracking.error ?? 'Live scheduler status', service.tracking.state === 'healthy' ? 'ok' : '')}
        ${renderMetricBox('Uptime', 'Not reported', 'No health-check source configured yet')}
        ${renderMetricBox('Collection interval', formatInterval(service.intervalSeconds), 'How often new metrics are collected')}
        ${renderMetricBox('Last collection', formatTimestamp(service.tracking.lastSuccess), service.tracking.lastError === undefined ? 'No collection errors recorded' : `Last error: ${formatTimestamp(service.tracking.lastError)}`)}
      </dl>

      <dl class="query-grid">
        ${renderQueryBox('Load signal', service.loadQuery)}
        ${renderQueryBox('Latency signal', service.latencyQuery)}
      </dl>
      <footer class="service-footer"><a class="secondary-button" href="#service/${String(service.id)}">View details</a></footer>
    </article>
  `).join('');

  renderShell(`
    <section class="summary-grid" aria-label="Service summary">
      ${renderSummaryCard('Total Services', services.length, 'total')}
      ${renderSummaryCard('Healthy', healthy, 'healthy')}
      ${renderSummaryCard('Degraded', degraded, 'degraded')}
      ${renderSummaryCard('Unavailable', unavailable, 'unavailable')}
    </section>
    <section class="service-panel">
      <div class="panel-header">
        <h2>Services</h2>
        <div class="panel-actions"><span>${String(services.length)} services loaded</span><a class="add-button" href="#add" aria-label="Add service"><span aria-hidden="true">+</span></a></div>
      </div>
      <div class="service-list">
        ${serviceRows}
      </div>
    </section>
  `, '200 OK');
  setServiceCount(services.length);
}

function renderAddChoice(): void {
  renderShell(`
    <section class="form-panel">
      <a class="back-link" href="#services">← Back to services</a>
      <p class="eyebrow">Add service</p>
      <h2>How should WeeWoo start tracking it?</h2>
      <p class="form-intro">Create a service from now on, or seed it with historical Prometheus data.</p>
      <div class="choice-grid">
        <a class="choice-card" href="#add/new"><strong>New service</strong><span>Start collecting at the next interval.</span></a>
        <a class="choice-card" href="#add/import"><strong>Import Prometheus history</strong><span>Collect an older time range, then continue live tracking.</span></a>
      </div>
    </section>
  `);
}

function inputValue(form: HTMLFormElement, name: string): string {
  const value = new FormData(form).get(name);
  return typeof value === 'string' ? value.trim() : '';
}

function serviceInputFromForm(form: HTMLFormElement): CreateServiceInput {
  return {
    name: inputValue(form, 'name'),
    prometheusUrl: inputValue(form, 'prometheusUrl'),
    loadQuery: inputValue(form, 'loadQuery'),
    latencyQuery: inputValue(form, 'latencyQuery'),
    intervalSeconds: Number(inputValue(form, 'intervalSeconds')),
  };
}

function renderServiceForm(importHistory: boolean): void {
  renderShell(`
    <section class="form-panel">
      <a class="back-link" href="#add">← Choose another option</a>
      <p class="eyebrow">${importHistory ? 'Import historical data' : 'New service'}</p>
      <h2>${importHistory ? 'Add a service with Prometheus history' : 'Add a service'}</h2>
      <p class="form-intro">All fields are required${importHistory ? ', including the historical UTC range' : ''}.</p>
      <form id="service-form" class="service-form">
        <label><span>Service name</span><input name="name" required autocomplete="organization" placeholder="Checkout API" /></label>
        <label><span>Prometheus URL</span><input name="prometheusUrl" required type="url" placeholder="https://prometheus.example.com" /></label>
        <label class="wide"><span>Load query</span><textarea name="loadQuery" required rows="3" placeholder="sum(rate(http_requests_total[5m]))"></textarea></label>
        <label class="wide"><span>Latency query</span><textarea name="latencyQuery" required rows="3" placeholder="histogram_quantile(0.95, ...)"></textarea></label>
        <label><span>Collection interval (seconds)</span><input name="intervalSeconds" required type="number" min="1" value="60" /></label>
        ${importHistory ? `
          <label><span>Import from</span><input name="importStart" required type="datetime-local" /></label>
          <label><span>Import through</span><input name="importEnd" required type="datetime-local" /></label>
        ` : ''}
        <div id="form-error" class="form-error wide" role="alert"></div>
        <div class="form-actions wide"><button id="test-service" class="secondary-button" type="button">Test connection</button><a class="secondary-button" href="#services">Cancel</a><button class="primary-button" type="submit">OK</button></div>
      </form>
    </section>
  `);

  document.querySelector<HTMLFormElement>('#service-form')?.addEventListener('submit', (event) => {
    event.preventDefault();
    void submitServiceForm(event.currentTarget as HTMLFormElement, importHistory);
  });
  document.querySelector<HTMLButtonElement>('#test-service')?.addEventListener('click', () => {
    const form = document.querySelector<HTMLFormElement>('#service-form');
    if (form !== null) void testServiceForm(form);
  });
}

async function testServiceForm(form: HTMLFormElement): Promise<void> {
  const button = form.querySelector<HTMLButtonElement>('#test-service');
  const errorBox = form.querySelector<HTMLElement>('#form-error');
  if (!form.reportValidity()) return;
  if (button !== null) { button.disabled = true; button.textContent = 'Testing…'; }
  try {
    const result = await TestService(serviceInputFromForm(form));
    if (errorBox !== null) {
      const load = result.loadQuery.valid ? `load: ${String(result.loadQuery.samples)} samples` : `load: ${result.loadQuery.error ?? 'failed'}`;
      const latency = result.latencyQuery.valid ? `latency: ${String(result.latencyQuery.samples)} samples` : `latency: ${result.latencyQuery.error ?? 'failed'}`;
      errorBox.classList.toggle('is-success', result.loadQuery.valid && result.latencyQuery.valid);
      errorBox.textContent = `${result.message} — ${load}; ${latency}`;
    }
  } catch (error) {
    if (errorBox !== null) { errorBox.classList.remove('is-success'); errorBox.textContent = error instanceof Error ? error.message : 'Test failed'; }
  } finally {
    if (button !== null) { button.disabled = false; button.textContent = 'Test connection'; }
  }
}

async function submitServiceForm(form: HTMLFormElement, importHistory: boolean): Promise<void> {
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const errorBox = form.querySelector<HTMLElement>('#form-error');
  const input = serviceInputFromForm(form);
  if (importHistory) {
    try {
      const range = historicalRangeToUtc(inputValue(form, 'importStart'), inputValue(form, 'importEnd'));
      input.importStart = range.start;
      input.importEnd = range.end;
    } catch (error) {
      if (errorBox !== null) errorBox.textContent = error instanceof Error ? error.message : 'Invalid import range.';
      return;
    }
  }
  if (submit !== null) {
    submit.disabled = true;
    submit.textContent = importHistory ? 'Importing…' : 'Adding…';
  }
  if (errorBox !== null) errorBox.textContent = '';
  try {
    await CreateService(input);
    window.location.hash = 'services';
  } catch (error) {
    if (errorBox !== null) errorBox.textContent = error instanceof Error ? error.message : 'Unable to add service.';
    if (submit !== null) {
      submit.disabled = false;
      submit.textContent = 'OK';
    }
  }
}

function renderPlaceholder(route: string): void {
  const title = route.charAt(0).toUpperCase() + route.slice(1);
  renderShell(`<section class="form-panel placeholder-panel"><p class="eyebrow">WeeWoo Services</p><h2>${escapeHtml(title)}</h2><p>This area is ready for its ${escapeHtml(route)} controls.</p><a class="primary-button" href="#services">Go to services</a></section>`);
}

function renderSettings(): void {
  const theme = savedTheme();
  renderShell(`
    <section class="settings-panel">
      <div class="settings-heading">
        <div>
          <p class="eyebrow">Preferences</p>
          <h2>Settings</h2>
          <p>Choose how the monitoring console looks on this device.</p>
        </div>
        <a class="secondary-button" href="#services">Back to services</a>
      </div>
      <div class="setting-row">
        <div>
          <strong>Appearance</strong>
          <p>Use a light interface, a dark interface, or follow your device setting.</p>
        </div>
        <div class="theme-picker" role="radiogroup" aria-label="Color theme">
          ${(['system', 'light', 'dark'] as Theme[]).map((option) => `
            <button type="button" class="theme-option${theme === option ? ' is-selected' : ''}" data-theme-option="${option}" role="radio" aria-checked="${String(theme === option)}">
              <span class="theme-preview theme-preview--${option}" aria-hidden="true"></span>
              ${option.charAt(0).toUpperCase() + option.slice(1)}
            </button>
          `).join('')}
        </div>
      </div>
    </section>
  `);
  document.querySelectorAll<HTMLButtonElement>('[data-theme-option]').forEach((button) => {
    button.addEventListener('click', () => {
      const selected = button.dataset.themeOption as Theme;
      localStorage.setItem('weewoo-theme', selected);
      applyTheme(selected);
      renderSettings();
    });
  });
}

function renderActivity(service: Service): string {
  if (service.tracking.activity.length === 0) return '<p class="muted-copy">No collection activity has been recorded yet.</p>';
  return `<ol class="activity-list">${service.tracking.activity.map((entry) => `
    <li><span class="activity-dot activity-dot--${escapeHtml(entry.type)}"></span><div><strong>${escapeHtml(entry.message)}</strong><time>${escapeHtml(formatTimestamp(entry.timestamp))}</time></div></li>
  `).join('')}</ol>`;
}

function renderImports(service: Service): string {
  if (service.imports.length === 0) return '<p class="muted-copy">No historical imports for this service.</p>';
  return `<div class="import-list">${service.imports.map((job) => `
    <article class="import-row">
      <div><strong>Import #${String(job.id)}</strong><span>${escapeHtml(statusLabel(job.state as Service['tracking']['state']))}</span></div>
      <progress max="100" value="${String(job.progress)}">${String(job.progress)}%</progress>
      ${job.totalWindows === 0 ? '' : `<p>${String(job.importedWindows)} of ${String(job.totalWindows)} windows imported${job.gapWindows === 0 ? '' : `; ${String(job.gapWindows)} monitoring gaps`}.</p>`}
      ${(job.state === 'queued' || job.state === 'running') ? `<button class="secondary-button cancel-import" type="button" data-import-id="${String(job.id)}">Cancel</button>` : ''}
      ${job.error === undefined ? '' : `<p>${escapeHtml(job.error)}</p>`}
    </article>
  `).join('')}</div>`;
}

function renderServiceHistory(history: ServiceChange[]): string {
  if (history.length === 0) return '<p class="muted-copy">No configuration changes recorded yet.</p>';
  return `<ol class="activity-list">${history.map((change) => `
    <li><span class="activity-dot"></span><div><strong>Revision ${String(change.newRevision)} by ${escapeHtml(change.changedBy)}</strong><time>${escapeHtml(formatTimestamp(change.changedAt))}</time><p>${change.material ? 'A new baseline generation was started.' : 'The existing baseline was retained.'}</p></div></li>
  `).join('')}</ol>`;
}

function renderServiceDetail(service: Service, history: ServiceChange[] = [], historyUnavailable = false): void {
  renderShell(`
    <section class="detail-header">
      <div><a class="back-link" href="#services">← All services</a><p class="eyebrow">Service #${String(service.id)}</p><h2>${escapeHtml(service.name)}</h2>${renderServiceUrl(service.prometheusUrl)}</div>
      <div class="detail-actions"><span class="status-pill status-pill--${escapeHtml(service.tracking.state)}">${escapeHtml(statusLabel(service.tracking.state))}</span><button id="toggle-tracking" class="secondary-button" type="button">${service.tracking.state === 'paused' ? 'Resume' : 'Pause'}</button><button id="reset-baseline" class="secondary-button" type="button">New service version</button><a class="secondary-button" href="#service/${String(service.id)}/edit">Edit</a><button id="delete-service" class="danger-button" type="button">Delete</button></div>
    </section>
    <section class="detail-grid">
      <article class="detail-card"><span>Tracking state</span><strong>${escapeHtml(statusLabel(service.tracking.state))}</strong><p>${escapeHtml(service.tracking.error ?? `Database revision ${String(service.revision ?? 1)}; active revision ${String(service.tracking.activeRevision ?? 'pending')}.`)}</p></article>
      <article class="detail-card"><span>Last successful collection</span><strong>${escapeHtml(formatTimestamp(service.tracking.lastSuccess))}</strong><p>Every ${escapeHtml(formatInterval(service.intervalSeconds))}</p></article>
      <article class="detail-card"><span>Last collection error</span><strong>${escapeHtml(formatTimestamp(service.tracking.lastError))}</strong><p>${escapeHtml(service.tracking.error ?? 'No errors recorded')}</p></article>
    </section>
    <section class="detail-columns">
      <article class="detail-panel"><div class="panel-header"><h2>Collection activity</h2><span>Latest first</span></div>${renderActivity(service)}</article>
      <article class="detail-panel"><div class="panel-header"><h2>Historical imports</h2><span>${String(service.imports.length)} jobs</span></div>${renderImports(service)}</article>
    </section>
    <section class="detail-panel query-detail"><div class="panel-header"><h2>Prometheus configuration</h2></div><dl class="query-grid">${renderQueryBox('Load signal', service.loadQuery)}${renderQueryBox('Latency signal', service.latencyQuery)}</dl></section>
    <section class="detail-panel"><div class="panel-header"><h2>Configuration history</h2><span>Revision ${String(service.revision ?? 1)} · generation ${String(service.generation ?? 1)}</span></div>${historyUnavailable ? '<p class="muted-copy">Configuration history is temporarily unavailable.</p>' : renderServiceHistory(history)}</section>
  `, '200 OK');
  document.querySelector('#delete-service')?.addEventListener('click', () => { void deleteServiceFromDetail(service); });
  document.querySelector('#reset-baseline')?.addEventListener('click', () => { void resetBaselineFromDetail(service); });
  document.querySelector('#toggle-tracking')?.addEventListener('click', () => {
    void (async () => { await SetServicePaused(service.id, service.tracking.state !== 'paused'); await loadServiceDetail(service.id); })();
  });
  document.querySelectorAll<HTMLButtonElement>('.cancel-import').forEach((button) => {
    button.addEventListener('click', () => {
      void (async () => {
        await CancelImport(Number(button.dataset.importId));
        await loadServiceDetail(service.id);
      })();
    });
  });
}

async function resetBaselineFromDetail(service: Service): Promise<void> {
  if (!window.confirm(`Reset the performance baseline for ${service.name}? The current Joint ECDF will be discarded and anomaly detection will learn from newly collected data.`)) return;
  await ResetServiceBaseline(service.id);
  await loadServiceDetail(service.id);
}

async function deleteServiceFromDetail(service: Service): Promise<void> {
  if (!window.confirm(`Delete ${service.name}? Historical data will be retained.`)) return;
  await DeleteService(service.id);
  window.location.hash = 'services';
}

async function loadServiceDetail(id: number, showLoading = true): Promise<void> {
  if (showLoading) renderLoading();
  try {
    const detail = await GetServiceDetail(id);
    if (currentRoute() === `service/${String(id)}`) renderServiceDetail(detail.service, detail.history, detail.historyUnavailable);
  } catch (error) {
    if (showLoading && currentRoute() === `service/${String(id)}`) renderError(error);
  }
}

function renderEditServiceForm(service: Service): void {
  renderShell(`
    <section class="form-panel"><a class="back-link" href="#service/${String(service.id)}">← Back to service</a><p class="eyebrow">Service #${String(service.id)}</p><h2>Edit service</h2>
      <form id="service-form" class="service-form">
        <label><span>Service name</span><input name="name" required value="${escapeHtml(service.name)}" /></label>
        <label><span>Prometheus URL</span><input name="prometheusUrl" required type="url" value="${escapeHtml(service.prometheusUrl)}" /></label>
        <label class="wide"><span>Load query</span><textarea name="loadQuery" required rows="3">${escapeHtml(service.loadQuery)}</textarea></label>
        <label class="wide"><span>Latency query</span><textarea name="latencyQuery" required rows="3">${escapeHtml(service.latencyQuery)}</textarea></label>
        <label><span>Collection interval (seconds)</span><input name="intervalSeconds" required type="number" min="1" value="${String(service.intervalSeconds)}" /></label>
        <div id="form-error" class="form-error wide" role="alert"></div>
        <div class="form-actions wide"><button id="test-service" class="secondary-button" type="button">Test connection</button><a class="secondary-button" href="#service/${String(service.id)}">Cancel</a><button class="primary-button" type="submit">Save changes</button></div>
      </form>
    </section>
  `);
  const form = document.querySelector<HTMLFormElement>('#service-form');
  form?.addEventListener('submit', (event) => {
    event.preventDefault();
    void (async () => {
      const errorBox = form.querySelector<HTMLElement>('#form-error');
      try {
        const input = serviceInputFromForm(form);
        input.revision = service.revision;
        await UpdateService(service.id, input);
        window.location.hash = `service/${String(service.id)}`;
      }
      catch (error) { if (errorBox !== null) errorBox.textContent = error instanceof Error ? error.message : 'Update failed'; }
    })();
  });
  form?.querySelector('#test-service')?.addEventListener('click', () => { void testServiceForm(form); });
}

async function loadEditService(id: number): Promise<void> {
  renderLoading();
  try { renderEditServiceForm(await GetService(id)); } catch (error) { renderError(error); }
}

function currentRoute(): string {
  return window.location.hash.slice(1) || 'services';
}

async function loadServices(showLoading = true): Promise<void> {
  if (showLoading) renderLoading();
  try {
    const services = await ListAllServices();
    if (currentRoute() === 'services') renderServices(services);
  } catch (error) {
    if (showLoading && currentRoute() === 'services') renderError(error);
  }
}

function clearLiveRefresh(): void {
  if (liveRefreshTimer !== undefined) {
    window.clearTimeout(liveRefreshTimer);
    liveRefreshTimer = undefined;
  }
}

async function refreshRoute(route: string): Promise<void> {
  if (route === 'alerts') {
    await loadAlerts(false);
    return;
  }
  if (route === 'services') {
    await loadServices(false);
    return;
  }
  const detailMatch = /^service\/(\d+)$/.exec(route);
  if (detailMatch !== null) await loadServiceDetail(Number(detailMatch[1]), false);
}

function scheduleLiveRefresh(route: string): void {
  clearLiveRefresh();
  const delay = liveRefreshDelay(route);
  if (delay === undefined) return;
  liveRefreshTimer = window.setTimeout(() => {
    liveRefreshTimer = undefined;
    if (currentRoute() !== route) return;
    if (document.hidden) {
      scheduleLiveRefresh(route);
      return;
    }
    void refreshRoute(route).finally(() => {
      if (currentRoute() === route) scheduleLiveRefresh(route);
    });
  }, delay);
}

async function boot(): Promise<void> {
  clearLiveRefresh();
  const route = currentRoute();
  if (route === 'add') { renderAddChoice(); return; }
  if (route === 'add/new') { renderServiceForm(false); return; }
  if (route === 'add/import') { renderServiceForm(true); return; }
  if (route === 'settings') { renderSettings(); return; }
  if (route === 'alerts') {
    await loadAlerts();
    if (currentRoute() === route) scheduleLiveRefresh(route);
    return;
  }
  const detailMatch = /^service\/(\d+)$/.exec(route);
  if (detailMatch !== null) {
    await loadServiceDetail(Number(detailMatch[1]));
    if (currentRoute() === route) scheduleLiveRefresh(route);
    return;
  }
  const editMatch = /^service\/(\d+)\/edit$/.exec(route);
  if (editMatch !== null) { await loadEditService(Number(editMatch[1])); return; }
  if (route !== 'services') { renderPlaceholder(route); return; }
  await loadServices();
  if (currentRoute() === route) scheduleLiveRefresh(route);
}

window.addEventListener('hashchange', () => { void boot(); });
document.addEventListener('visibilitychange', () => {
  const route = currentRoute();
  if (!document.hidden && liveRefreshDelay(route) !== undefined) {
    clearLiveRefresh();
    void refreshRoute(route).finally(() => {
      if (currentRoute() === route) scheduleLiveRefresh(route);
    });
  }
});
void boot();
