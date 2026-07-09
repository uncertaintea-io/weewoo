export interface Service {
  id: number;
  name: string;
  prometheusUrl: string;
  loadQuery: string;
  latencyQuery: string;
  intervalSeconds: number;
}

type Fetcher = typeof fetch;

export class ServicesApiError extends Error {
  public readonly status: number;
  public readonly statusText: string;

  public constructor(status: number, statusText: string) {
    const responseLabel = `${String(status)}${statusText === '' ? '' : ` ${statusText}`}`;
    super(`Unable to load services. Server returned ${responseLabel}.`);
    this.name = 'ServicesApiError';
    this.status = status;
    this.statusText = statusText;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function readString(value: unknown, field: string): string {
  if (typeof value !== 'string') {
    throw new Error(`Service response field "${field}" must be a string.`);
  }
  return value;
}

function readNumber(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new Error(`Service response field "${field}" must be a number.`);
  }
  return value;
}

function parseService(value: unknown): Service {
  if (!isRecord(value)) {
    throw new Error('Service response item must be an object.');
  }

  return {
    id: readNumber(value.id, 'id'),
    name: readString(value.name, 'name'),
    prometheusUrl: readString(value.prometheusUrl, 'prometheusUrl'),
    loadQuery: readString(value.loadQuery, 'loadQuery'),
    latencyQuery: readString(value.latencyQuery, 'latencyQuery'),
    intervalSeconds: readNumber(value.intervalSeconds, 'intervalSeconds'),
  };
}

export async function ListAllServices(fetcher: Fetcher = fetch): Promise<Service[]> {
  const response = await fetcher('/api/services', {
    headers: {
      Accept: 'application/json',
    },
  });

  if (!response.ok) {
    throw new ServicesApiError(response.status, response.statusText);
  }

  const responseBody: unknown = await response.json();
  if (!Array.isArray(responseBody)) {
    throw new Error('Service response must be an array.');
  }

  return responseBody.map(parseService);
}
