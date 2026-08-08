import {
  alertCDFComparison,
  GetAlertEvidence,
  ServicesApiError,
  type AlertCDFComparison,
  type AlertEvidence,
} from './api';

export type AlertPDFState =
  | { status: 'not-applicable' }
  | { status: 'loading' }
  | { status: 'available'; comparison: AlertCDFComparison }
  | { status: 'unavailable'; message: string };

type AlertEvidenceLoader = (occurrenceID: number) => Promise<AlertEvidence>;

export function anomalyOccurrence<T extends { kind: string }>(occurrences: readonly T[]): T | undefined {
  return occurrences.find((occurrence) => occurrence.kind === 'anomaly');
}

export async function resolveAlertPDFState(
  occurrenceID: number,
  loadEvidence: AlertEvidenceLoader = GetAlertEvidence,
): Promise<AlertPDFState> {
  try {
    return {
      status: 'available',
      comparison: alertCDFComparison(await loadEvidence(occurrenceID)),
    };
  } catch (error) {
    if (error instanceof ServicesApiError && error.status === 410) {
      return {
        status: 'unavailable',
        message: 'The reference distribution used for this alert is no longer retained.',
      };
    }
    return {
      status: 'unavailable',
      message: error instanceof Error ? error.message : 'Unable to load Alert Evidence.',
    };
  }
}
