// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

import { expect } from 'chai';
import 'mocha';
import { CreateService, GetAlertEvidence, GetServiceDetail, ListAlerts, ListAllServices, ResetServiceBaseline, ReviewAlertOccurrence, ServicesApiError } from './api';
import { datetimeLocalToUtcISOString, historicalRangeToUtc } from './datetime';
import {
  LIVE_REFRESH_MILLISECONDS,
  LIVE_REFRESH_OFFSET_MILLISECONDS,
  liveRefreshDelay,
} from './live-refresh';
import { searchValueForRender } from './navigation';
import {
  alertCardClasses,
  collectionUptime,
  groupAlertsByStatus,
  orderedAlertEvidence,
  renderServiceUrl,
  reviewableAnomalousOccurrencesByService,
} from './rendering';

describe('datetimeLocalToUtcISOString', () => {

  it('interprets a timezone-less form value as UTC', () => {
    expect(datetimeLocalToUtcISOString('2026-07-01T00:00')).to.equal('2026-07-01T00:00:00.000Z');
  });

});

describe('historicalRangeToUtc', () => {

  it('returns a strictly increasing UTC range', () => {
    expect(historicalRangeToUtc('2026-07-29T12:00', '2026-07-30T12:00')).to.deep.equal({
      start: '2026-07-29T12:00:00.000Z',
      end: '2026-07-30T12:00:00.000Z',
    });
  });

  it('rejects a range whose start is not before its end', () => {
    expect(() => historicalRangeToUtc('2026-07-30T12:00', '2026-07-30T11:00'))
      .to.throw('Import start must be before import end.');
  });

});

describe('liveRefreshDelay', () => {

  it('refreshes data-backed pages two seconds after each thirty-second boundary', () => {
    const atThirtyTwoSeconds = LIVE_REFRESH_MILLISECONDS + LIVE_REFRESH_OFFSET_MILLISECONDS;
    const atFiftyNineSeconds = 59_000;

    expect(liveRefreshDelay('services', atThirtyTwoSeconds)).to.equal(LIVE_REFRESH_MILLISECONDS);
    expect(liveRefreshDelay('service/42', atFiftyNineSeconds)).to.equal(3_000);
    expect(liveRefreshDelay('alerts', 61_000)).to.equal(1_000);
  });

  it('does not refresh static pages or forms', () => {
    expect(liveRefreshDelay('settings', 59_000)).to.equal(undefined);
    expect(liveRefreshDelay('service/42/edit', 59_000)).to.equal(undefined);
    expect(liveRefreshDelay('add/new', 59_000)).to.equal(undefined);
  });

});

describe('searchValueForRender', () => {

  it('preserves a filter only when refreshing the same route', () => {
    expect(searchValueForRender('services', 'services', 'checkout')).to.equal('checkout');
    expect(searchValueForRender('services', 'alerts', 'checkout')).to.equal('');
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

describe('alertCardClasses', () => {

  it('marks resolved alerts independently of their previous severity', () => {
    expect(alertCardClasses('critical', 'resolved')).to.equal(
      'alert-card alert-card--critical alert-card--resolved',
    );
    expect(alertCardClasses('warning', 'resolved')).to.equal(
      'alert-card alert-card--warning alert-card--resolved',
    );
  });

});

describe('collectionUptime', () => {

  it('reports elapsed collection time from the tracking start', () => {
    expect(collectionUptime('healthy', '2026-08-06T12:00:00Z', new Date('2026-08-06T14:03:00Z'))).to.equal('2h 3m');
  });

  it('reports unavailable before collection has started', () => {
    expect(collectionUptime('pending', undefined, new Date('2026-08-06T14:03:00Z'))).to.equal('Unavailable');
  });

  it('does not report elapsed collection time while tracking is paused', () => {
    expect(collectionUptime('paused', '2026-08-06T12:00:00Z', new Date('2026-08-06T14:03:00Z'))).to.equal('Unavailable');
  });

});

describe('groupAlertsByStatus', () => {

  it('separates active alerts from resolved history', () => {
    const alerts = [
      { id: 1, status: 'resolved' },
      { id: 2, status: 'firing' },
      { id: 3, status: 'resolved' },
    ];

    const grouped = groupAlertsByStatus(alerts);

    expect(grouped.active.map((alert) => alert.id)).to.deep.equal([2]);
    expect(grouped.resolved.map((alert) => alert.id)).to.deep.equal([1, 3]);
  });

});

describe('orderedAlertEvidence', () => {

  it('orders useful anomaly evidence and removes duplicate and historical fields', () => {
    const entries = orderedAlertEvidence({
      historical: true,
      indicator: 2,
      load: 14,
      pValue: 0,
      threshold: 0.01,
    });

    expect(entries).to.deep.equal([
      { label: 'Indicator', value: 2 },
      { label: 'Load', value: 14 },
      { label: 'Observed p-value', value: 0 },
      { label: 'Alerting threshold', value: 0.01 },
    ]);
  });

  it('preserves the observed p-value from each occurrence', () => {
    const first = orderedAlertEvidence({ pValue: 0.001 });
    const second = orderedAlertEvidence({ pValue: 0.2 });

    expect(first).to.deep.equal([{ label: 'Observed p-value', value: 0.001 }]);
    expect(second).to.deep.equal([{ label: 'Observed p-value', value: 0.2 }]);
  });

});

describe('reviewableAnomalousOccurrencesByService', () => {

  it('separates unaccepted Bad chunks by service', () => {
    const targets = reviewableAnomalousOccurrencesByService([
      {
        serviceId: 10,
        serviceName: 'checkout',
        status: 'firing',
        occurrences: [
          { id: 1, reviewRevision: 0, chunkTimestamp: '2026-07-30T12:00:00Z' },
          { id: 2, reviewRevision: 1, chunkTimestamp: '2026-07-30T12:01:00Z', reviewOverride: true },
          { id: 3, reviewRevision: 0 },
        ],
      },
      {
        serviceId: 20,
        serviceName: 'catalog',
        status: 'firing',
        occurrences: [
          { id: 4, reviewRevision: 2, chunkTimestamp: '2026-07-30T12:02:00Z' },
        ],
      },
      {
        serviceId: 10,
        serviceName: 'checkout',
        status: 'resolved',
        occurrences: [
          { id: 5, reviewRevision: 0, chunkTimestamp: '2026-07-29T12:00:00Z' },
        ],
      },
    ]);

    expect(targets).to.deep.equal([
      { serviceKey: '10', serviceName: 'checkout', targets: [{ id: 1, revision: 0 }] },
      { serviceKey: '20', serviceName: 'catalog', targets: [{ id: 4, revision: 2 }] },
    ]);
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

describe('GetServiceDetail', () => {

  it('loads the service when configuration history is unavailable', async () => {
    const fetcher = (url: string | URL | Request): Promise<Response> => {
      if (url === '/api/services/1') {
        return Promise.resolve(new Response(JSON.stringify({
          id: 1,
          name: 'checkout',
          prometheusUrl: 'http://prometheus.example.com',
          loadQuery: 'load',
          latencyQuery: 'latency',
          intervalSeconds: 30,
        }), { status: 200 }));
      }
      expect(url).to.equal('/api/services/1/history');
      return Promise.resolve(new Response('history unavailable', { status: 500 }));
    };

    const detail = await GetServiceDetail(1, fetcher);

    expect(detail.service.name).to.equal('checkout');
    expect(detail.history).to.deep.equal([]);
    expect(detail.historyUnavailable).to.equal(true);
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

describe('ResetServiceBaseline', () => {

  it('starts a new service performance generation', async () => {
    const fetcher = (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
      expect(url).to.equal('/api/services/7/baseline-reset');
      expect(init?.method).to.equal('POST');
      return Promise.resolve(new Response(JSON.stringify({
        id: 7,
        name: 'checkout',
        prometheusUrl: 'http://prometheus.example.com',
        loadQuery: 'load',
        latencyQuery: 'latency',
        intervalSeconds: 30,
        revision: 2,
        generation: 2,
      }), { status: 200 }));
    };

    const service = await ResetServiceBaseline(7, fetcher);

    expect(service.generation).to.equal(2);
    expect(service.revision).to.equal(2);
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

  it('loads Alert Evidence for an anomaly occurrence', async () => {
    const fetcher = (url: string | URL | Request): Promise<Response> => {
      expect(url).to.equal('/api/alerts/occurrences/12/evidence');
      return Promise.resolve(new Response(JSON.stringify({
        query: { input: 125.4, xs: [0.1, 0.2], ps: [0.25, 0.75] },
        samples: [{ value: 0.18, count: 91 }],
        pValue: 0.001,
      }), { status: 200 }));
    };

    const evidence = await GetAlertEvidence(12, fetcher);

    expect(evidence.query).to.deep.equal({ input: 125.4, xs: [0.1, 0.2], ps: [0.25, 0.75] });
    expect(evidence.samples).to.deep.equal([{ value: 0.18, count: 91 }]);
    expect(evidence.pValue).to.equal(0.001);
  });

});
