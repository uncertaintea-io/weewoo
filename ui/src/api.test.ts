import { expect } from 'chai';
import 'mocha';
import { GetJointECDF } from './api';

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
      })));
    };

    const render = await GetJointECDF(7, 9, 2, fetcher);

    expect(render).to.deep.equal({
      width: 2,
      height: 2,
      xMin: 10,
      xMax: 20,
      yMin: 100,
      yMax: 200,
      masses: [0.1, 0.2, 0.3, 0.4],
    });
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
    })));

    try {
      await GetJointECDF(1, 1, 0, fetcher);
      expect.fail('Expected GetJointECDF to reject.');
    } catch (error) {
      expect((error as Error).message).to.equal('Joint ECDF response must contain 4 cell masses.');
    }
  });

});
