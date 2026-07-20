import './index.scss'
import { CreateService, ListAllServices, ServicesApiError, type CreateServiceInput, type Service } from './api';
import { escapeHtml, renderServiceUrl } from './rendering';

const app = document.querySelector<HTMLDivElement>('#app');
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

function renderShell(content: string, apiResponse = 'Ready'): void {
  if (app === null) {
    return;
  }

  app.innerHTML = `
    <div class="app-frame">
      <aside class="sidebar" aria-label="Primary navigation">
        <div class="sidebar-brand">
          <img class="brand-logo" src="/img/UncertainTEA.png" alt="" aria-hidden="true" />
          <div>
            <strong>WeeWoo Services</strong>
            <span>Monitoring console</span>
          </div>
        </div>
        <nav class="sidebar-nav">
          <a class="${window.location.hash === '#settings' ? '' : 'is-active'}" href="#services">Services</a>
          <a href="#alerts">Alerts</a>
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
}

function bindShellInteractions(): void {
  const search = document.querySelector<HTMLInputElement>('.search-box input');
  search?.addEventListener('input', () => {
    const term = search.value.trim().toLowerCase();
    document.querySelectorAll<HTMLElement>('.service-dashboard').forEach((row) => {
      row.hidden = term !== '' && !(row.dataset.serviceName ?? '').includes(term);
    });
  });
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

  const serviceRows = services.map((service) => `
    <article class="service-dashboard" data-service-name="${escapeHtml(service.name.toLowerCase())}">
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
        ${renderMetricBox('Collection interval', formatInterval(service.intervalSeconds), 'How often new metrics are collected')}
        ${renderMetricBox('Last alert', 'None reported', 'Alert history API not available yet')}
      </dl>

      <dl class="query-grid">
        ${renderQueryBox('Load signal', service.loadQuery)}
        ${renderQueryBox('Latency signal', service.latencyQuery)}
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
        <div class="form-actions wide"><a class="secondary-button" href="#services">Cancel</a><button class="primary-button" type="submit">OK</button></div>
      </form>
    </section>
  `);

  document.querySelector<HTMLFormElement>('#service-form')?.addEventListener('submit', (event) => {
    event.preventDefault();
    void submitServiceForm(event.currentTarget as HTMLFormElement, importHistory);
  });
}

async function submitServiceForm(form: HTMLFormElement, importHistory: boolean): Promise<void> {
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const errorBox = form.querySelector<HTMLElement>('#form-error');
  const input: CreateServiceInput = {
    name: inputValue(form, 'name'),
    prometheusUrl: inputValue(form, 'prometheusUrl'),
    loadQuery: inputValue(form, 'loadQuery'),
    latencyQuery: inputValue(form, 'latencyQuery'),
    intervalSeconds: Number(inputValue(form, 'intervalSeconds')),
  };
  if (importHistory) {
    input.importStart = new Date(inputValue(form, 'importStart')).toISOString();
    input.importEnd = new Date(inputValue(form, 'importEnd')).toISOString();
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

async function boot(): Promise<void> {
  const route = window.location.hash.slice(1) || 'services';
  if (route === 'add') { renderAddChoice(); return; }
  if (route === 'add/new') { renderServiceForm(false); return; }
  if (route === 'add/import') { renderServiceForm(true); return; }
  if (route === 'settings') { renderSettings(); return; }
  if (route !== 'services') { renderPlaceholder(route); return; }
  renderLoading();

  try {
    const services = await ListAllServices();
    renderServices(services);
  } catch (error) {
    renderError(error);
  }
}

window.addEventListener('hashchange', () => { void boot(); });
void boot();
