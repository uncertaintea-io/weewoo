import { expect } from 'chai';
import 'mocha';
import { viridis } from './colormap';
import { densityPixels, formatLatencySeconds, jointECDFRenderCacheKey, normalizeMasses, renderAxisValue } from './jecdf';

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

  it('maps render positions onto linear and logarithmic dataset axes', () => {
    expect(renderAxisValue(10, 110, 0.5, false)).to.equal(60);
    expect(renderAxisValue(1, 100, 0.5, true)).to.be.closeTo(10, 1e-12);
    expect(renderAxisValue(0.001, 1000, 0.25, true)).to.be.closeTo(0.0316227766, 1e-10);
  });

  it('preserves axis endpoints and clamps positions outside the rendered image', () => {
    expect(renderAxisValue(1, 100, -1, true)).to.equal(1);
    expect(renderAxisValue(1, 100, 2, true)).to.equal(100);
  });

  it('does not share cached renders across service generations', () => {
    const current = jointECDFRenderCacheKey(7, 3, 1, 2);
    expect(jointECDFRenderCacheKey(7, 3, 1, 2)).to.equal(current);
    expect(jointECDFRenderCacheKey(7, 4, 1, 2)).not.to.equal(current);
    expect(jointECDFRenderCacheKey(8, 3, 1, 2)).not.to.equal(current);
  });

});
