// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

import { expect } from 'chai';
import 'mocha';
import { interpolatePDF, logarithmicRangeForSelection, samplePDFSegments } from './alert-pdf';

describe('alert PDF interpolation', () => {

  it('differentiates every monotonic CDF interpolation segment', () => {
    const segments = interpolatePDF([
      { x: 0, probability: 0 },
      { x: 1, probability: 1 },
      { x: 2, probability: 1 },
    ]);

    expect(segments).to.have.length(2);
    expect(segments[0]?.pdf.eval(0)).to.equal(0);
    expect(segments[0]?.pdf.eval(0.5)).to.be.greaterThan(0);
    expect(segments[1]?.pdf.eval(1.5)).to.equal(0);
  });

});

describe('alert PDF x-axis selection', () => {

  it('maps a reversed drag onto an ordered logarithmic range', () => {
    const [minimum, maximum] = logarithmicRangeForSelection(1, 100, 0.75, 0.25);

    expect(minimum).to.be.closeTo(Math.sqrt(10), 0.000001);
    expect(maximum).to.be.closeTo(Math.sqrt(1000), 0.000001);
  });

  it('clamps a drag to the current view bounds', () => {
    const [minimum, maximum] = logarithmicRangeForSelection(1, 100, -1, 2);

    expect(minimum).to.equal(1);
    expect(maximum).to.be.closeTo(100, 0.000001);
  });

});

describe('alert PDF viewport sampling', () => {

  it('resamples derivative functions across the requested logarithmic view', () => {
    const pdf = { eval: (x: number) => x, deriv: () => pdf };
    const segments = [{ x1: 1, x2: 100, pdf }];

    const full = samplePDFSegments(segments, 1, 100, 4);
    const zoomed = samplePDFSegments(segments, 10, 20, 4);

    const expectedFullX = [1, Math.sqrt(10), 10, Math.sqrt(1000), 100];
    full.forEach((point, index) => {
      expect(point.x).to.be.closeTo(expectedFullX[index], 0.000001);
    });
    expect(zoomed[0]?.x).to.equal(10);
    expect(zoomed[2]?.x).to.be.closeTo(Math.sqrt(200), 0.000001);
    expect(zoomed[4]?.x).to.be.closeTo(20, 0.000001);
    expect(zoomed.map((point) => point.density)).to.deep.equal(zoomed.map((point) => point.x));
  });

});
