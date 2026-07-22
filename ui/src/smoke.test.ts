import { expect } from 'chai';
import 'mocha';
import { CreateService, ListAllServices, ServicesApiError } from './api';
import { datetimeLocalToUtcISOString } from './datetime';
import { renderServiceUrl } from './rendering';

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
