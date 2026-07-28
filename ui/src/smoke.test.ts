import { expect } from 'chai';
import 'mocha';
import { CreateService, ListAlerts, ListAllServices, ReviewAlertOccurrence, ServicesApiError } from './api';
import { datetimeLocalToUtcISOString } from './datetime';
import { alertVisualClasses, renderServiceUrl } from './rendering';

describe('datetimeLocalToUtcISOString', () => {

  it('interprets a timezone-less form value as UTC', () => {
    expect(datetimeLocalToUtcISOString('2026-07-01T00:00')).to.equal('2026-07-01T00:00:00.000Z');
  });

});

describe('Exercise the testing framework itself', () => {

  it('should pass', () => {
    expect(true).to.equal(true);
  });

});

describe('renderServiceUrl', () => {

  it('renders http and https URLs as links', () => {
    expect(renderServiceUrl('https://prometheus.example.com')).to.equal(
      '<a class="service-url" href="https://prometheus.example.com" target="_blank" rel="noreferrer">https://prometheus.example.com</a>',
    );
  });

  it('renders unsafe URL schemes as plain text', () => {
    expect(renderServiceUrl('javascript:alert(1)')).to.equal(
      '<span class="service-url">javascript:alert(1)</span>',
    );
  });

});

describe('alertVisualClasses', () => {

  it('uses warning accents for an active warning alert', () => {
    expect(alertVisualClasses('warning', 'firing')).to.deep.equal({
      card: 'alert-card alert-card--warning',
      severity: 'severity-pill severity-pill--warning',
      status: 'alert-status alert-status--firing',
    });
  });

  it('uses critical accents for an active critical alert', () => {
    expect(alertVisualClasses('critical', 'firing')).to.deep.equal({
      card: 'alert-card alert-card--critical',
      severity: 'severity-pill severity-pill--critical',
      status: 'alert-status alert-status--firing',
    });
  });

  it('makes resolved status take precedence over critical severity', () => {
    expect(alertVisualClasses('critical', 'resolved')).to.deep.equal({
      card: 'alert-card alert-card--resolved',
      severity: 'severity-pill severity-pill--resolved',
      status: 'alert-status alert-status--resolved',
    });
  });

  it('makes resolved status take precedence over warning severity', () => {
    expect(alertVisualClasses('warning', 'resolved')).to.deep.equal({
      card: 'alert-card alert-card--resolved',
      severity: 'severity-pill severity-pill--resolved',
      status: 'alert-status alert-status--resolved',
    });
  });

});

describe('ListAllServices', () => {

  it('reads services from the API', async () => {
    const fetcher = (url: string | URL | Request): Promise<Response> => {
      expect(url).to.equal('/api/services');
      return Promise.resolve(new Response(JSON.stringify([
        {
          id: 1,
          name: 'checkout',
          prometheusUrl: 'http://prometheus.example.com',
          loadQuery: 'load',
          latencyQuery: 'latency',
          intervalSeconds: 30,
        },
      ]), {
        status: 200,
        headers: {
          'Content-Type': 'application/json',
        },
      }));
    };

    const services = await ListAllServices(fetcher);

    expect(services).to.deep.equal([
      {
        id: 1,
        name: 'checkout',
        prometheusUrl: 'http://prometheus.example.com',
        loadQuery: 'load',
        latencyQuery: 'latency',
        intervalSeconds: 30,
        tracking: { state: 'pending', activity: [] },
        imports: [],
      },
    ]);
  });

  it('reports unsuccessful API status details', async () => {
    const fetcher = (): Promise<Response> => Promise.resolve(new Response(null, {
      status: 503,
      statusText: 'Service Unavailable',
    }));

    try {
      await ListAllServices(fetcher);
      expect.fail('Expected ListAllServices to reject.');
    } catch (error) {
      expect(error).to.be.instanceOf(ServicesApiError);
      expect((error as ServicesApiError).status).to.equal(503);
      expect((error as ServicesApiError).statusText).to.equal('Service Unavailable');
    }
  });

});

describe('CreateService', () => {

  it('posts a service and reads the created response', async () => {
    const input = {
      name: 'checkout', prometheusUrl: 'http://prometheus.example.com',
      loadQuery: 'load', latencyQuery: 'latency', intervalSeconds: 30,
    };
    const fetcher = (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
      expect(url).to.equal('/api/services');
      expect(init?.method).to.equal('POST');
      expect(init?.body).to.be.a('string');
      expect(JSON.parse(init?.body as string)).to.deep.equal(input);
      return Promise.resolve(new Response(JSON.stringify({ id: 7, ...input }), { status: 201 }));
    };

    const service = await CreateService(input, fetcher);

    expect(service.id).to.equal(7);
    expect(service.name).to.equal('checkout');
  });

});

describe('Alerts API', () => {

  it('reads durable alert history', async () => {
    const fetcher = (url: string | URL | Request): Promise<Response> => {
      expect(url).to.equal('/api/alerts?history=true');
      return Promise.resolve(new Response(JSON.stringify([{
        id: 9,
        serviceName: 'checkout',
        kind: 'anomaly',
        severity: 'warning',
        status: 'firing',
        title: 'Anomalous service behavior detected',
        description: 'A time chunk differed from the reference.',
        impact: 'Latency may be unusual.',
        suggestedAction: 'Inspect the service.',
        technicalDetails: '',
        startedAt: '2026-07-24T10:00:00Z',
        lastOccurredAt: '2026-07-24T10:00:00Z',
        occurrenceCount: 1,
        consecutiveCount: 1,
        alertmanagerState: 'accepted',
        occurrences: [{
          id: 12,
          kind: 'anomaly',
          occurredAt: '2026-07-24T10:00:00Z',
          detectedAt: '2026-07-24T10:01:00Z',
          chunkTimestamp: '2026-07-24T10:00:00Z',
          summary: 'Anomalous time chunk',
          technicalDetails: '',
          evidence: { pValue: 0.001 },
          reviewRevision: 0,
        }],
        events: [],
      }])));
    };

    const alerts = await ListAlerts(true, fetcher);

    expect(alerts).to.have.length(1);
    expect(alerts[0]?.occurrences[0]?.evidence.pValue).to.equal(0.001);
  });

  it('submits an optimistic chunk review', async () => {
    const fetcher = (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
      expect(url).to.equal('/api/alerts/occurrences/12/review');
      expect(init?.method).to.equal('POST');
      expect(JSON.parse(init?.body as string)).to.deep.equal({
        revision: 3,
        accepted: true,
        reason: 'planned deployment',
      });
      return Promise.resolve(new Response('{}', { status: 200 }));
    };

    await ReviewAlertOccurrence(12, 3, true, 'planned deployment', fetcher);
  });

});
