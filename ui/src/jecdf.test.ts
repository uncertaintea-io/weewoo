import { expect } from 'chai';
import 'mocha';
import { viridis } from './colormap';
import { densityPixels, formatLatencySeconds, normalizeMasses } from './jecdf';

describe('Joint ECDF density rendering', () => {

  it('normalizes masses relative to the densest cell before gamma correction', () => {
    expect(normalizeMasses([0, 0.25, 1], 0.5)).to.deep.equal([0, 0.5, 1]);
    expect(normalizeMasses([0, 0], 0.5)).to.deep.equal([0, 0]);
  });

  it('preserves API image-row order while mapping masses through Viridis', () => {
    const pixels = densityPixels({
      width: 2,
      height: 2,
      xMin: 0,
      xMax: 1,
      yMin: 0,
      yMax: 1,
      masses: [0, 1, 0.25, 0.5],
    }, 1);

    expect(Array.from(pixels.slice(0, 4))).to.deep.equal(Object.values(viridis(0)));
    expect(Array.from(pixels.slice(4, 8))).to.deep.equal(Object.values(viridis(1)));
    expect(Array.from(pixels.slice(8, 12))).to.deep.equal(Object.values(viridis(0.25)));
    expect(Array.from(pixels.slice(12, 16))).to.deep.equal(Object.values(viridis(0.5)));
  });

  it('formats latency values supplied in seconds with human-readable units', () => {
    expect(formatLatencySeconds(2.5)).to.equal('2.5s');
    expect(formatLatencySeconds(0.001)).to.equal('1ms');
    expect(formatLatencySeconds(0.0001)).to.equal('100µs');
    expect(formatLatencySeconds(0.00000025)).to.equal('250ns');
  });

});
