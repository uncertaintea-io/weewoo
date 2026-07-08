import './index.scss'
import { ListAllServices, ServicesApiError, type Service } from './api';
import { escapeHtml, renderServiceUrl } from './rendering';

const app = document.querySelector<HTMLDivElement>('#app');

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

function renderShell(content: string, apiResponse = 'Loading'): void {
  if (app === null) {
    return;
  }

  app.innerHTML = `
    <div class="app-frame">
      <aside class="sidebar" aria-label="Primary navigation">
        <div class="sidebar-brand">
          <span class="brand-mark" aria-hidden="true"></span>
          <div>
            <strong>WeeWoo Services</strong>
            <span>Monitoring console</span>
          </div>
        </div>
        <nav class="sidebar-nav">
          <a class="is-active" href="#">Overview</a>
          <a href="#">Services</a>
          <a href="#">Alerts</a>
          <a href="#">Incidents</a>
          <a href="#">Integrations</a>
          <a href="#">Settings</a>
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
            <button class="icon-button" type="button" aria-label="Settings">
              <span aria-hidden="true"></span>
            </button>
            <div class="avatar" aria-label="User profile"></div>
          </div>
        </header>
        <main class="page-shell">
          <section class="page-header">
            <div>
              <p class="eyebrow">WeeWoo Services</p>
              <h1>Service health dashboard</h1>
              <p>Live from the WeeWoo server configuration API.</p>
            </div>
            <article class="api-status-card">
              <div>
                <span>API Endpoint</span>
                <strong>/api/services</strong>
              </div>
              <div>
                <span>Response</span>
                <strong class="api-response">${escapeHtml(apiResponse)}</strong>
              </div>
            </article>
          </section>
          ${content}
        </main>
      </div>
    </div>
  `;
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

  const serviceRows = services.map((service) => `
    <article class="service-dashboard">
      <header class="service-header">
        <div class="service-identity">
          <div class="service-avatar" aria-hidden="true">${escapeHtml(serviceInitial(service))}</div>
          <div>
            <div class="service-title">
              <h2>${escapeHtml(service.name)}</h2>
              <span class="service-id">#${String(service.id)}</span>
            </div>
            ${renderServiceUrl(service.prometheusUrl)}
          </div>
        </div>
        <span class="status-pill status-pill--healthy">Healthy</span>
      </header>

      <dl class="metric-grid">
        ${renderMetricBox('Current status', 'Healthy', 'Loaded from service configuration', 'ok')}
        ${renderMetricBox('Uptime', 'Not reported', 'No health-check source configured yet')}
        ${renderMetricBox('Collection interval', formatInterval(service.intervalSeconds), 'Prometheus polling cadence')}
        ${renderMetricBox('Last alert', 'None reported', 'Alert history API not available yet')}
      </dl>

      <dl class="query-grid">
        ${renderQueryBox('Measuring load', service.loadQuery)}
        ${renderQueryBox('Measuring latency', service.latencyQuery)}
      </dl>
    </article>
  `).join('');

  renderShell(`
    <section class="summary-grid" aria-label="Service summary">
      ${renderSummaryCard('Total Services', services.length, 'total')}
      ${renderSummaryCard('Healthy', services.length, 'healthy')}
      ${renderSummaryCard('Degraded', 0, 'degraded')}
      ${renderSummaryCard('Unavailable', 0, 'unavailable')}
    </section>
    <section class="service-panel">
      <div class="panel-header">
        <h2>Services</h2>
        <span>${String(services.length)} services loaded</span>
      </div>
      <div class="service-list">
        ${serviceRows}
      </div>
    </section>
  `, '200 OK');
  setServiceCount(services.length);
}

async function boot(): Promise<void> {
  renderLoading();

  try {
    const services = await ListAllServices();
    renderServices(services);
  } catch (error) {
    renderError(error);
  }
}

void boot();
