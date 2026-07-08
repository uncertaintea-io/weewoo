import { expect } from 'chai';
import 'mocha';
import { ListAllServices } from './api';

describe('Exercise the testing framework itself', () => {

  it('should pass', () => {
    expect(true).to.equal(true);
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
      },
    ]);
  });

});
