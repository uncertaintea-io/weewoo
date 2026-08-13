import { searchValueForRender } from './navigation';
import { escapeHtml } from './rendering';

export type Theme = 'light' | 'dark' | 'system';

export interface PageMeta {
  eyebrow?: string;
  title?: string;
  description?: string;
  endpoint?: string;
}

export class AppShell {
  private lastRenderedRoute: string | undefined;

  constructor(
    private readonly root: HTMLDivElement | null,
    private readonly currentRoute: () => string,
  ) {}

  savedTheme(): Theme {
    const theme = localStorage.getItem('weewoo-theme');
    return theme === 'light' || theme === 'dark' ? theme : 'system';
  }

  applyTheme(theme: Theme): void {
    if (theme === 'system') {
      document.documentElement.removeAttribute('data-theme');
    } else {
      document.documentElement.dataset.theme = theme;
    }
  }

  render(content: string, apiResponse = 'Ready', page: PageMeta = {}): void {
    if (this.root === null) return;

    const route = this.currentRoute();
    const searchValue = searchValueForRender(
      this.lastRenderedRoute,
      route,
      document.querySelector<HTMLInputElement>('.search-box input')?.value ?? '',
    );
    this.lastRenderedRoute = route;

    this.root.innerHTML = `
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
            <a class="${window.location.hash.startsWith('#alert') ? 'is-active' : ''}" href="#alerts">Alerts</a>
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
              <input type="search" name="search" placeholder="Search services" />
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

    this.bindSearch();
    const search = document.querySelector<HTMLInputElement>('.search-box input');
    if (search !== null && searchValue !== '') {
      search.value = searchValue;
      search.dispatchEvent(new Event('input'));
    }
  }

  renderStandalone(content: string): void {
    if (this.root !== null) this.root.innerHTML = content;
  }

  setServiceCount(count: number): void {
    const label = count === 1 ? '1 service monitored' : `${String(count)} services monitored`;
    document.querySelector('#service-count')?.replaceChildren(label);
  }

  private bindSearch(): void {
    const search = document.querySelector<HTMLInputElement>('.search-box input');
    search?.addEventListener('input', () => {
      const term = search.value.trim().toLowerCase();
      document.querySelectorAll<HTMLElement>('.service-dashboard, .alert-card').forEach((row) => {
        row.hidden = term !== '' && !(row.dataset.serviceName ?? '').includes(term);
      });
    });
  }
}
