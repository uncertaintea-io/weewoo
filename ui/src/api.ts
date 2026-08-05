export interface Service {
  id: number;
  name: string;
  prometheusUrl: string;
  loadQuery: string;
  latencyQuery: string;
  intervalSeconds: number;
  revision?: number;
  generation?: number;
  baselineResetAt?: string;
  tracking: TrackingStatus;
  imports: ImportJob[];
  timeOfDayModel?: TimeOfDayModelStatus;
}

export interface TimeOfDayModelStatus {
  state: 'learning' | 'ready' | 'degraded';
  coverage: number;
  requiredDays: number;
  latestBuild?: string;
}

export interface ActivityEntry {
  type: string;
  message: string;
  timestamp: string;
}

export interface TrackingStatus {
  state: 'pending' | 'collecting' | 'healthy' | 'degraded' | 'unavailable' | 'paused';
  lastSuccess?: string;
  lastError?: string;
  error?: string;
  activeRevision?: number;
  activity: ActivityEntry[];
}

export interface ImportJob {
  id: number;
  serviceId: number;
  state: 'queued' | 'running' | 'complete' | 'complete_with_gaps' | 'failed' | 'cancelled';
  progress: number;
  totalWindows: number;
  importedWindows: number;
  gapWindows: number;
  error?: string;
  startedAt: string;
  endedAt?: string;
}

export interface CreateServiceInput {
  name: string;
  prometheusUrl: string;
  loadQuery: string;
  latencyQuery: string;
  intervalSeconds: number;
  revision?: number;
  importStart?: string;
  importEnd?: string;
}

export interface ServiceChange {
  serviceId: number;
  previousRevision: number;
  newRevision: number;
  changedAt: string;
  changedBy: string;
  material: boolean;
}

export interface ServiceTestResult {
  message: string;
  loadQuery: { valid: boolean; samples: number; latest?: number; error?: string };
  latencyQuery: { valid: boolean; samples: number; latest?: number; error?: string };
}

export interface ServiceDetail {
  service: Service;
  history: ServiceChange[];
  historyUnavailable: boolean;
}

export interface AlertOccurrence {
  id: number;
  kind: string;
  occurredAt: string;
  detectedAt: string;
  windowStart?: string;
  windowEnd?: string;
  chunkTimestamp?: string;
  summary: string;
  technicalDetails: string;
  evidence: Record<string, unknown>;
  reviewRevision: number;
  reviewOverride?: boolean;
  reviewedAt?: string;
  reviewReason?: string;
}

export interface AlertEvent {
  type: string;
  message: string;
  metadata: Record<string, unknown>;
  occurredAt: string;
}

export interface AlertRecord {
  id: number;
  serviceId?: number;
  serviceName: string;
  indicatorId?: number;
  kind: string;
  severity: 'info' | 'warning' | 'critical';
  status: 'firing' | 'resolved';
  title: string;
  description: string;
  impact: string;
  suggestedAction: string;
  technicalDetails: string;
  startedAt: string;
  lastOccurredAt: string;
  resolvedAt?: string;
  resolutionReason?: string;
  occurrenceCount: number;
  consecutiveCount: number;
  alertmanagerState: 'pending' | 'accepted' | 'failed' | 'missed';
  alertmanagerError?: string;
  occurrences: AlertOccurrence[];
  events: AlertEvent[];
}

export interface CDFSample {
  value: number;
  count: number;
}

export interface AlertOccurrenceCDF {
  schemaVersion: number;
  alertId: number;
  occurrenceId: number;
  serviceId: number;
  serviceGeneration: number;
  indicatorId: number;
  chunkTimestamp: string;
  x: CDFSample[];
  y: CDFSample[];
  cdf: { status: 'not_implemented'; description: string };
}

export interface JointECDFRender {
  width: number;
  height: number;
  xMin: number;
  xMax: number;
  yMin: number;
  yMax: number;
  /** Image-row order: X varies fastest and rows run from yMax to yMin. */
  masses: number[];
}

export interface JointECDFRequestOptions {
  renderOptions?: number;
  ifNoneMatch?: string;
  fetcher?: Fetcher;
}

export type JointECDFFetchResult =
  | { modified: false; etag: string }
  | { modified: true; etag: string; render: JointECDFRender };

export const JECDF_RENDER_OPTION_LOG_X = 1;
export const JECDF_RENDER_OPTION_LOG_Y = 2;

type Fetcher = typeof fetch;

export class ServicesApiError extends Error {
  public readonly status: number;
  public readonly statusText: string;

  public constructor(status: number, statusText: string) {
    const responseLabel = `${String(status)}${statusText === '' ? '' : ` ${statusText}`}`;
    super(`Services API returned ${responseLabel}.`);
    this.name = 'ServicesApiError';
    this.status = status;
    this.statusText = statusText;
  }
}

async function readServiceError(response: Response): Promise<ServicesApiError> {
  const error = new ServicesApiError(response.status, response.statusText);
  const detail = (await response.text()).trim();
  if (detail !== '') {
    error.message = detail;
  }
  return error;
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

  const trackingValue = isRecord(value.tracking) ? value.tracking : {};
  const activity = Array.isArray(trackingValue.activity) ? trackingValue.activity.filter(isRecord).map((entry) => ({
    type: readString(entry.type, 'activity.type'),
    message: readString(entry.message, 'activity.message'),
    timestamp: readString(entry.timestamp, 'activity.timestamp'),
  })) : [];
  const imports = Array.isArray(value.imports) ? value.imports.filter(isRecord).map((job) => ({
    id: readNumber(job.id, 'import.id'),
    serviceId: readNumber(job.serviceId, 'import.serviceId'),
    state: readString(job.state, 'import.state') as ImportJob['state'],
    progress: readNumber(job.progress, 'import.progress'),
    totalWindows: typeof job.totalWindows === 'number' ? job.totalWindows : 0,
    importedWindows: typeof job.importedWindows === 'number' ? job.importedWindows : 0,
    gapWindows: typeof job.gapWindows === 'number' ? job.gapWindows : 0,
    ...(typeof job.error === 'string' ? { error: job.error } : {}),
    startedAt: readString(job.startedAt, 'import.startedAt'),
    ...(typeof job.endedAt === 'string' ? { endedAt: job.endedAt } : {}),
  })) : [];
  const timeOfDayValue = isRecord(value.timeOfDayModel) ? value.timeOfDayModel : {};
  return {
    id: readNumber(value.id, 'id'),
    name: readString(value.name, 'name'),
    prometheusUrl: readString(value.prometheusUrl, 'prometheusUrl'),
    loadQuery: readString(value.loadQuery, 'loadQuery'),
    latencyQuery: readString(value.latencyQuery, 'latencyQuery'),
    intervalSeconds: readNumber(value.intervalSeconds, 'intervalSeconds'),
    ...(typeof value.revision === 'number' ? { revision: value.revision } : {}),
    ...(typeof value.generation === 'number' ? { generation: value.generation } : {}),
    ...(typeof value.baselineResetAt === 'string' ? { baselineResetAt: value.baselineResetAt } : {}),
    tracking: {
      state: (typeof trackingValue.state === 'string' ? trackingValue.state : 'pending') as TrackingStatus['state'],
      ...(typeof trackingValue.lastSuccess === 'string' ? { lastSuccess: trackingValue.lastSuccess } : {}),
      ...(typeof trackingValue.lastError === 'string' ? { lastError: trackingValue.lastError } : {}),
      ...(typeof trackingValue.error === 'string' ? { error: trackingValue.error } : {}),
      ...(typeof trackingValue.activeRevision === 'number' ? { activeRevision: trackingValue.activeRevision } : {}),
      activity,
    },
    imports,
    ...(isRecord(value.timeOfDayModel) ? { timeOfDayModel: {
      state: (typeof timeOfDayValue.state === 'string' ? timeOfDayValue.state : 'learning') as TimeOfDayModelStatus['state'],
      coverage: typeof timeOfDayValue.coverage === 'number' ? timeOfDayValue.coverage : 0,
      requiredDays: typeof timeOfDayValue.requiredDays === 'number' ? timeOfDayValue.requiredDays : 5,
      ...(typeof timeOfDayValue.latestBuild === 'string' ? { latestBuild: timeOfDayValue.latestBuild } : {}),
    } } : {}),
  };
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined;
}

function parseAlertOccurrence(value: unknown): AlertOccurrence {
  if (!isRecord(value)) throw new Error('Alert occurrence must be an object.');
  return {
    id: readNumber(value.id, 'occurrence.id'),
    kind: readString(value.kind, 'occurrence.kind'),
    occurredAt: readString(value.occurredAt, 'occurrence.occurredAt'),
    detectedAt: readString(value.detectedAt, 'occurrence.detectedAt'),
    ...(optionalString(value.windowStart) === undefined ? {} : { windowStart: value.windowStart as string }),
    ...(optionalString(value.windowEnd) === undefined ? {} : { windowEnd: value.windowEnd as string }),
    ...(optionalString(value.chunkTimestamp) === undefined ? {} : { chunkTimestamp: value.chunkTimestamp as string }),
    summary: readString(value.summary, 'occurrence.summary'),
    technicalDetails: readString(value.technicalDetails, 'occurrence.technicalDetails'),
    evidence: isRecord(value.evidence) ? value.evidence : {},
    reviewRevision: readNumber(value.reviewRevision, 'occurrence.reviewRevision'),
    ...(typeof value.reviewOverride === 'boolean' ? { reviewOverride: value.reviewOverride } : {}),
    ...(optionalString(value.reviewedAt) === undefined ? {} : { reviewedAt: value.reviewedAt as string }),
    ...(optionalString(value.reviewReason) === undefined ? {} : { reviewReason: value.reviewReason as string }),
  };
}

function parseAlert(value: unknown): AlertRecord {
  if (!isRecord(value)) throw new Error('Alert response item must be an object.');
  const occurrences = Array.isArray(value.occurrences) ? value.occurrences.map(parseAlertOccurrence) : [];
  const events: AlertEvent[] = Array.isArray(value.events) ? value.events.filter(isRecord).map((event) => ({
    type: readString(event.type, 'event.type'),
    message: readString(event.message, 'event.message'),
    metadata: isRecord(event.metadata) ? event.metadata : {},
    occurredAt: readString(event.occurredAt, 'event.occurredAt'),
  })) : [];
  return {
    id: readNumber(value.id, 'alert.id'),
    ...(typeof value.serviceId === 'number' ? { serviceId: value.serviceId } : {}),
    serviceName: readString(value.serviceName, 'alert.serviceName'),
    ...(typeof value.indicatorId === 'number' ? { indicatorId: value.indicatorId } : {}),
    kind: readString(value.kind, 'alert.kind'),
    severity: readString(value.severity, 'alert.severity') as AlertRecord['severity'],
    status: readString(value.status, 'alert.status') as AlertRecord['status'],
    title: readString(value.title, 'alert.title'),
    description: readString(value.description, 'alert.description'),
    impact: readString(value.impact, 'alert.impact'),
    suggestedAction: readString(value.suggestedAction, 'alert.suggestedAction'),
    technicalDetails: readString(value.technicalDetails, 'alert.technicalDetails'),
    startedAt: readString(value.startedAt, 'alert.startedAt'),
    lastOccurredAt: readString(value.lastOccurredAt, 'alert.lastOccurredAt'),
    ...(optionalString(value.resolvedAt) === undefined ? {} : { resolvedAt: value.resolvedAt as string }),
    ...(optionalString(value.resolutionReason) === undefined ? {} : { resolutionReason: value.resolutionReason as string }),
    occurrenceCount: readNumber(value.occurrenceCount, 'alert.occurrenceCount'),
    consecutiveCount: readNumber(value.consecutiveCount, 'alert.consecutiveCount'),
    alertmanagerState: readString(value.alertmanagerState, 'alert.alertmanagerState') as AlertRecord['alertmanagerState'],
    ...(optionalString(value.alertmanagerError) === undefined ? {} : { alertmanagerError: value.alertmanagerError as string }),
    occurrences,
    events,
  };
}

function parseJointECDFRender(value: unknown): JointECDFRender {
  if (!isRecord(value)) {
    throw new Error('Joint ECDF response must be an object.');
  }

  const width = readNumber(value.width, 'width');
  const height = readNumber(value.height, 'height');
  if (!Number.isInteger(width) || width < 2 || !Number.isInteger(height) || height < 2) {
    throw new Error('Joint ECDF response dimensions must be integers of at least 2.');
  }

  const xMin = readNumber(value.xMin, 'xMin');
  const xMax = readNumber(value.xMax, 'xMax');
  const yMin = readNumber(value.yMin, 'yMin');
  const yMax = readNumber(value.yMax, 'yMax');
  if (xMin > xMax || yMin > yMax) {
    throw new Error('Joint ECDF response bounds are invalid.');
  }
  if (!Array.isArray(value.masses) || value.masses.length !== width * height) {
    throw new Error(`Joint ECDF response must contain ${String(width * height)} cell masses.`);
  }
  const masses = value.masses.map((mass, index) => {
    const parsed = readNumber(mass, `masses[${String(index)}]`);
    if (parsed < 0 || parsed > 1) {
      throw new Error(`Joint ECDF response mass at index ${String(index)} must be between 0 and 1.`);
    }
    return parsed;
  });

  return { width, height, xMin, xMax, yMin, yMax, masses };
}

async function serviceRequest(path: string, init: RequestInit, fetcher: Fetcher): Promise<Service> {
  const response = await fetcher(path, init);
  if (!response.ok) throw await readServiceError(response);
  return parseService(await response.json());
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

export async function CreateService(input: CreateServiceInput, fetcher: Fetcher = fetch): Promise<Service> {
  return serviceRequest('/api/services', {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(input),
  }, fetcher);
}

export async function GetService(id: number, fetcher: Fetcher = fetch): Promise<Service> {
  return serviceRequest(`/api/services/${String(id)}`, { headers: { Accept: 'application/json' } }, fetcher);
}

export async function ListServiceHistory(id: number, fetcher: Fetcher = fetch): Promise<ServiceChange[]> {
  const response = await fetcher(`/api/services/${String(id)}/history`, { headers: { Accept: 'application/json' } });
  if (!response.ok) throw await readServiceError(response);
  const body: unknown = await response.json();
  if (!Array.isArray(body)) throw new Error('Service history response must be an array.');
  return body.filter(isRecord).map((change) => ({
    serviceId: readNumber(change.serviceId, 'history.serviceId'),
    previousRevision: readNumber(change.previousRevision, 'history.previousRevision'),
    newRevision: readNumber(change.newRevision, 'history.newRevision'),
    changedAt: readString(change.changedAt, 'history.changedAt'),
    changedBy: readString(change.changedBy, 'history.changedBy'),
    material: change.material === true,
  }));
}

export async function GetServiceDetail(id: number, fetcher: Fetcher = fetch): Promise<ServiceDetail> {
  const service = await GetService(id, fetcher);
  try {
    return { service, history: await ListServiceHistory(id, fetcher), historyUnavailable: false };
  } catch {
    return { service, history: [], historyUnavailable: true };
  }
}

export async function UpdateService(id: number, input: CreateServiceInput, fetcher: Fetcher = fetch): Promise<Service> {
  return serviceRequest(`/api/services/${String(id)}`, {
    method: 'PUT', headers: { Accept: 'application/json', 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  }, fetcher);
}

export async function DeleteService(id: number, fetcher: Fetcher = fetch): Promise<void> {
  const response = await fetcher(`/api/services/${String(id)}`, { method: 'DELETE' });
  if (!response.ok) throw await readServiceError(response);
}

export async function TestService(input: CreateServiceInput, fetcher: Fetcher = fetch): Promise<ServiceTestResult> {
  const response = await fetcher('/api/services/test', {
    method: 'POST', headers: { Accept: 'application/json', 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  });
  const body: unknown = await response.json();
  if (!isRecord(body) || !isRecord(body.loadQuery) || !isRecord(body.latencyQuery)) {
    if (!response.ok) throw new ServicesApiError(response.status, response.statusText);
    throw new Error('Query test response is invalid.');
  }
  const parseResult = (value: Record<string, unknown>) => ({
    valid: value.valid === true,
    samples: typeof value.samples === 'number' ? value.samples : 0,
    ...(typeof value.latest === 'number' ? { latest: value.latest } : {}),
    ...(typeof value.error === 'string' ? { error: value.error } : {}),
  });
  return {
    message: typeof body.message === 'string' ? body.message : 'Query test complete',
    loadQuery: parseResult(body.loadQuery),
    latencyQuery: parseResult(body.latencyQuery),
  };
}

export async function CancelImport(id: number, fetcher: Fetcher = fetch): Promise<void> {
  const response = await fetcher(`/api/imports/${String(id)}/cancel`, { method: 'POST' });
  if (!response.ok) throw await readServiceError(response);
}

export async function SetServicePaused(id: number, paused: boolean, fetcher: Fetcher = fetch): Promise<Service> {
  return serviceRequest(`/api/services/${String(id)}/${paused ? 'pause' : 'resume'}`, { method: 'POST', headers: { Accept: 'application/json' } }, fetcher);
}

export async function ResetServiceBaseline(id: number, fetcher: Fetcher = fetch): Promise<Service> {
  return serviceRequest(`/api/services/${String(id)}/baseline-reset`, { method: 'POST', headers: { Accept: 'application/json' } }, fetcher);
}

export async function ListAlerts(includeHistory = true, fetcher: Fetcher = fetch): Promise<AlertRecord[]> {
  const response = await fetcher(`/api/alerts?history=${String(includeHistory)}`, { headers: { Accept: 'application/json' } });
  if (!response.ok) throw await readServiceError(response);
  const body: unknown = await response.json();
  if (!Array.isArray(body)) throw new Error('Alerts response must be an array.');
  return body.map(parseAlert);
}

export async function GetAlertOccurrenceCDF(
  occurrenceId: number,
  fetcher: Fetcher = fetch,
): Promise<AlertOccurrenceCDF> {
  const response = await fetcher(`/api/alerts/occurrences/${String(occurrenceId)}/cdf`, {
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) throw await readServiceError(response);
  const body: unknown = await response.json();
  if (!isRecord(body) || !Array.isArray(body.x) || !Array.isArray(body.y)
    || typeof body.chunkTimestamp !== 'string' || !isRecord(body.cdf)
    || typeof body.serviceGeneration !== 'number'
    || typeof body.cdf.description !== 'string') {
    throw new Error('Alert occurrence CDF response is invalid.');
  }
  const parseSamples = (samples: unknown[]): CDFSample[] => samples.map((sample) => {
    if (!isRecord(sample) || typeof sample.value !== 'number' || typeof sample.count !== 'number') {
      throw new Error('Alert occurrence CDF samples are invalid.');
    }
    return { value: sample.value, count: sample.count };
  });
  return {
    schemaVersion: Number(body.schemaVersion), alertId: Number(body.alertId), occurrenceId: Number(body.occurrenceId),
    serviceId: Number(body.serviceId), serviceGeneration: body.serviceGeneration,
    indicatorId: Number(body.indicatorId), chunkTimestamp: body.chunkTimestamp,
    x: parseSamples(body.x), y: parseSamples(body.y),
    cdf: { status: 'not_implemented', description: body.cdf.description },
  };
}

export async function GetJointECDF(
  serviceID: number,
  indicatorID: number,
  request: JointECDFRequestOptions = {},
): Promise<JointECDFFetchResult> {
  const options = request.renderOptions ?? 0;
  const optionsQuery = options === 0
    ? ''
    : `&options=${encodeURIComponent(String(options))}`;
  const headers = new Headers({ Accept: 'application/json' });
  if (request.ifNoneMatch !== undefined) headers.set('If-None-Match', request.ifNoneMatch);
  const response = await (request.fetcher ?? fetch)(
    `/api/jecdf?serviceId=${encodeURIComponent(String(serviceID))}&indicatorId=${encodeURIComponent(String(indicatorID))}${optionsQuery}`,
    { headers },
  );
  const etag = response.headers.get('ETag') ?? request.ifNoneMatch;
  if (response.status === 304) {
    if (etag === undefined) throw new Error('Joint ECDF 304 response must include an ETag.');
    return { modified: false, etag };
  }
  if (!response.ok) throw await readServiceError(response);
  if (etag === undefined) throw new Error('Joint ECDF response must include an ETag.');
  return { modified: true, etag, render: parseJointECDFRender(await response.json()) };
}

export async function ReviewAlertOccurrence(
  occurrenceId: number,
  revision: number,
  accepted: boolean,
  reason: string,
  fetcher: Fetcher = fetch,
): Promise<void> {
  const response = await fetcher(`/api/alerts/occurrences/${String(occurrenceId)}/review`, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ revision, accepted, reason }),
  });
  if (!response.ok) throw await readServiceError(response);
}
