// SPDX-FileCopyrightText: 2026 Uncertain Tea Inc.
// SPDX-License-Identifier: LicenseRef-PolyForm-Internal-Use-1.0.0

import { expect } from 'chai';
import 'mocha';
import { anomalyOccurrence, resolveAlertPDFState } from './alert-detail';
import { ServicesApiError } from './api';

describe('alert detail PDF state', () => {

  it('does not select PDF evidence for non-anomaly alerts', () => {
    const occurrence = anomalyOccurrence([
      { id: 1, kind: 'collection_failure' },
      { id: 2, kind: 'monitoring_impaired' },
    ]);

    expect(occurrence).to.equal(undefined);
  });

  it('degrades only the PDF when its reference distribution has expired', async () => {
    const state = await resolveAlertPDFState(12, () => (
      Promise.reject(new ServicesApiError(410, 'Gone'))
    ));

    expect(state).to.deep.equal({
      status: 'unavailable',
      message: 'The reference distribution used for this alert is no longer retained.',
    });
  });

  it('builds an available PDF state from occurrence evidence', async () => {
    const state = await resolveAlertPDFState(12, () => Promise.resolve({
      query: { input: 10, xs: [1, 2], ps: [0.25, 1] },
      samples: [{ value: 1, count: 1 }, { value: 2, count: 3 }],
      pValue: 0.001,
    }));

    expect(state).to.deep.equal({
      status: 'available',
      comparison: {
        expected: [{ x: 1, probability: 0.25 }, { x: 2, probability: 1 }],
        actual: [{ x: 1, probability: 0.25 }, { x: 2, probability: 1 }],
      },
    });
  });

});
