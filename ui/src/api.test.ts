import { expect } from 'chai';
import 'mocha';
import { GetAlertCDFComparison, GetJointECDF } from './api';

describe('Alert CDF proof-of-concept API', () => {

  it('combines the expected CDF and weighted analysis samples', async () => {
    const fetcher = (url: string | URL | Request): Promise<Response> => {
      if (url === '/fixtures/last-query.json') {
        return Promise.resolve(new Response(JSON.stringify({ xs: [1, 2], ps: [0.25, 1] })));
      }
      expect(url).to.equal('/fixtures/last-analysis.json');
      return Promise.resolve(new Response(JSON.stringify({
        latency_sample: [
          { Value: 2, Count: 3 },
          { Value: 1, Count: 1 },
        ],
      })));
    };

    const comparison = await GetAlertCDFComparison(fetcher);

    expect(comparison).to.deep.equal({
      expected: [
        { x: 1, probability: 0.25 },
        { x: 2, probability: 1 },
      ],
      actual: [
        { x: 1, probability: 0.25 },
        { x: 2, probability: 1 },
      ],
      pValue: 0,
    });
  });

});

describe('Joint ECDF API', () => {

  it('requests and parses a rendered joint ECDF', async () => {
    const fetcher = (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
      expect(url).to.equal('/api/jecdf?serviceId=7&indicatorId=9&options=2');
      expect(new Headers(init?.headers).get('Accept')).to.equal('application/json');
      return Promise.resolve(new Response(JSON.stringify({
        width: 2,
        height: 2,
        xMin: 10,
        xMax: 20,
        yMin: 100,
        yMax: 200,
        masses: [0.1, 0.2, 0.3, 0.4],
      }), { headers: { ETag: '"definition-1"' } }));
    };

    const result = await GetJointECDF(7, 9, { renderOptions: 2, fetcher });

    expect(result).to.deep.equal({
      modified: true,
      etag: '"definition-1"',
      render: {
        width: 2,
        height: 2,
        xMin: 10,
        xMax: 20,
        yMin: 100,
        yMax: 200,
        masses: [0.1, 0.2, 0.3, 0.4],
      },
    });
  });

  it('sends a validator and accepts a not-modified response', async () => {
    const fetcher = (_url: string | URL | Request, init?: RequestInit): Promise<Response> => {
      expect(new Headers(init?.headers).get('If-None-Match')).to.equal('"definition-1"');
      return Promise.resolve(new Response(null, {
        status: 304,
        headers: { ETag: '"definition-1"' },
      }));
    };

    const result = await GetJointECDF(7, 9, {
      ifNoneMatch: '"definition-1"',
      fetcher,
    });

    expect(result).to.deep.equal({ modified: false, etag: '"definition-1"' });
  });

  it('rejects a response with the wrong number of cell masses', async () => {
    const fetcher = (): Promise<Response> => Promise.resolve(new Response(JSON.stringify({
      width: 2,
      height: 2,
      xMin: 0,
      xMax: 1,
      yMin: 0,
      yMax: 1,
      masses: [0.5],
    }), { headers: { ETag: '"definition-1"' } }));

    try {
      await GetJointECDF(1, 1, { fetcher });
      expect.fail('Expected GetJointECDF to reject.');
    } catch (error) {
      expect((error as Error).message).to.equal('Joint ECDF response must contain 4 cell masses.');
    }
  });

});
