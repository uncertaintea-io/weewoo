import { type AlertCDFComparison, type CDFPoint } from './api';
import { interpolateMonotonic, type Func } from './func';

const MARGIN = { top: 24, right: 24, bottom: 62, left: 54 };
const SAMPLES_PER_SEGMENT = 12;
const MINIMUM_SELECTION_WIDTH = 8;

interface PlotArea {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface DragSelection {
  pointerID: number;
  startX: number;
  currentX: number;
}

export interface PDFSegment {
  x1: number;
  x2: number;
  pdf: Func;
}

export function interpolatePDF(points: CDFPoint[]): PDFSegment[] {
  if (points.length < 2) return [];
  const sorted = [...points].sort((left, right) => left.x - right.x);
  const xs = sorted.map((point) => point.x);
  const probabilities = sorted.map((point) => point.probability);
  return interpolateMonotonic(xs, probabilities).map((cdf, index) => ({
    x1: xs[index],
    x2: xs[index + 1],
    pdf: cdf.deriv(),
  }));
}

export function logarithmicRangeForSelection(
  minimum: number,
  maximum: number,
  startRatio: number,
  endRatio: number,
): [number, number] {
  if (minimum <= 0 || maximum <= minimum) {
    throw new RangeError('A logarithmic selection requires positive, increasing bounds.');
  }
  const clamp = (ratio: number): number => Math.min(Math.max(ratio, 0), 1);
  const valueAt = (ratio: number): number => minimum * Math.exp(clamp(ratio) * Math.log(maximum / minimum));
  const start = valueAt(startRatio);
  const end = valueAt(endRatio);
  return start <= end ? [start, end] : [end, start];
}

function formatLatency(seconds: number): string {
  const format = (value: number): string => new Intl.NumberFormat(undefined, { maximumSignificantDigits: 2 }).format(value);
  if (seconds >= 1) return `${format(seconds)}s`;
  if (seconds >= 0.001) return `${format(seconds * 1000)}ms`;
  return `${format(seconds * 1_000_000)}µs`;
}

function cssColor(canvas: HTMLCanvasElement, name: string, fallback: string): string {
  return getComputedStyle(canvas).getPropertyValue(name).trim() || fallback;
}

function sampleSegments(segments: PDFSegment[]): { x: number; density: number }[] {
  return segments.flatMap((segment) => {
    const samples = new Array<{ x: number; density: number }>(SAMPLES_PER_SEGMENT + 1);
    for (let index = 0; index <= SAMPLES_PER_SEGMENT; index += 1) {
      const ratio = index / SAMPLES_PER_SEGMENT;
      const x = segment.x1 + (segment.x2 - segment.x1) * ratio;
      samples[index] = { x, density: Math.max(0, segment.pdf.eval(x)) };
    }
    return samples;
  });
}

class AlertPDFPlot {
  private readonly context: CanvasRenderingContext2D;
  private readonly resizeObserver: ResizeObserver | null;
  private readonly expected: { x: number; density: number }[];
  private readonly actual: { x: number; density: number }[];
  private readonly fullMinimum: number;
  private readonly fullMaximum: number;
  private viewMinimum: number;
  private viewMaximum: number;
  private plotArea: PlotArea | null = null;
  private dragSelection: DragSelection | null = null;

  public constructor(
    private readonly canvas: HTMLCanvasElement,
    comparison: AlertCDFComparison,
    private readonly resetButton: HTMLButtonElement | null,
  ) {
    const context = canvas.getContext('2d');
    if (context === null) throw new Error('Unable to create the alert PDF canvas context.');
    this.context = context;
    this.expected = sampleSegments(interpolatePDF(comparison.expected));
    this.actual = sampleSegments(interpolatePDF(comparison.actual));
    const positiveX = [...this.expected, ...this.actual].map((point) => point.x).filter((x) => x > 0);
    this.fullMinimum = Math.min(...positiveX);
    this.fullMaximum = Math.max(...positiveX);
    this.viewMinimum = this.fullMinimum;
    this.viewMaximum = this.fullMaximum;
    this.resizeObserver = typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(this.draw);
    this.resizeObserver?.observe(canvas);
    window.addEventListener('resize', this.draw);
    this.canvas.addEventListener('pointerdown', this.handlePointerDown);
    this.canvas.addEventListener('pointermove', this.handlePointerMove);
    this.canvas.addEventListener('pointerup', this.handlePointerUp);
    this.canvas.addEventListener('pointercancel', this.handlePointerCancel);
    this.resetButton?.addEventListener('click', this.handleReset);
    if (this.resetButton !== null) this.resetButton.hidden = true;
    this.draw();
  }

  public destroy(): void {
    this.resizeObserver?.disconnect();
    window.removeEventListener('resize', this.draw);
    this.canvas.removeEventListener('pointerdown', this.handlePointerDown);
    this.canvas.removeEventListener('pointermove', this.handlePointerMove);
    this.canvas.removeEventListener('pointerup', this.handlePointerUp);
    this.canvas.removeEventListener('pointercancel', this.handlePointerCancel);
    this.resetButton?.removeEventListener('click', this.handleReset);
  }

  private readonly draw = (): void => {
    const cssWidth = Math.max(this.canvas.clientWidth, 1);
    const cssHeight = Math.max(this.canvas.clientHeight, 1);
    const pixelRatio = window.devicePixelRatio || 1;
    const renderWidth = Math.round(cssWidth * pixelRatio);
    const renderHeight = Math.round(cssHeight * pixelRatio);
    if (this.canvas.width !== renderWidth || this.canvas.height !== renderHeight) {
      this.canvas.width = renderWidth;
      this.canvas.height = renderHeight;
    }
    this.context.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0);
    this.context.clearRect(0, 0, cssWidth, cssHeight);

    const area = {
      x: MARGIN.left,
      y: MARGIN.top,
      width: Math.max(cssWidth - MARGIN.left - MARGIN.right, 1),
      height: Math.max(cssHeight - MARGIN.top - MARGIN.bottom, 1),
    };
    this.plotArea = area;
    const allPoints = [...this.expected, ...this.actual];
    const minimum = this.viewMinimum;
    const maximum = this.viewMaximum;
    const visiblePoints = allPoints.filter((point) => point.x >= minimum && point.x <= maximum);
    const maximumDensity = Math.max(0, ...visiblePoints.map((point) => point.density));
    if (!Number.isFinite(minimum) || !Number.isFinite(maximum) || !Number.isFinite(maximumDensity)) return;

    this.drawAxes(area, minimum, maximum);
    // The actual area is the backdrop; the expected line is drawn above it.
    const actualColor = cssColor(this.canvas, '--alert-pdf-actual', '#c47a44');
    const expectedColor = cssColor(this.canvas, '--alert-pdf-expected', '#2563eb');
    this.drawSeries(this.actual, area, minimum, maximum, maximumDensity, 'transparent', actualColor);
    this.drawSeries(this.expected, area, minimum, maximum, maximumDensity, expectedColor, '');
    this.drawDragSelection(area);
  };

  private drawDragSelection(area: PlotArea): void {
    if (this.dragSelection === null) return;
    const start = Math.min(this.dragSelection.startX, this.dragSelection.currentX);
    const end = Math.max(this.dragSelection.startX, this.dragSelection.currentX);
    this.context.save();
    this.context.fillStyle = 'rgba(37, 99, 235, 0.12)';
    this.context.strokeStyle = 'rgba(37, 99, 235, 0.48)';
    this.context.lineWidth = 1;
    this.context.fillRect(start, area.y, end - start, area.height);
    this.context.strokeRect(start + 0.5, area.y + 0.5, Math.max(0, end - start - 1), Math.max(0, area.height - 1));
    this.context.restore();
  }

  private clampPlotX(x: number, area: PlotArea): number {
    return Math.min(Math.max(x, area.x), area.x + area.width);
  }

  private readonly handlePointerDown = (event: PointerEvent): void => {
    const area = this.plotArea;
    if (area === null || event.button !== 0) return;
    const bounds = this.canvas.getBoundingClientRect();
    const x = event.clientX - bounds.left;
    const y = event.clientY - bounds.top;
    if (x < area.x || x > area.x + area.width || y < area.y || y > area.y + area.height) return;
    const plotX = this.clampPlotX(x, area);
    this.dragSelection = { pointerID: event.pointerId, startX: plotX, currentX: plotX };
    this.canvas.setPointerCapture(event.pointerId);
    this.canvas.classList.add('is-selecting');
    event.preventDefault();
    this.draw();
  };

  private readonly handlePointerMove = (event: PointerEvent): void => {
    const area = this.plotArea;
    if (area === null || this.dragSelection?.pointerID !== event.pointerId) return;
    const bounds = this.canvas.getBoundingClientRect();
    this.dragSelection.currentX = this.clampPlotX(event.clientX - bounds.left, area);
    event.preventDefault();
    this.draw();
  };

  private readonly handlePointerUp = (event: PointerEvent): void => {
    const area = this.plotArea;
    const selection = this.dragSelection;
    if (area === null || selection?.pointerID !== event.pointerId) return;
    const bounds = this.canvas.getBoundingClientRect();
    selection.currentX = this.clampPlotX(event.clientX - bounds.left, area);
    const selectionWidth = Math.abs(selection.currentX - selection.startX);
    this.dragSelection = null;
    this.canvas.classList.remove('is-selecting');
    if (this.canvas.hasPointerCapture(event.pointerId)) this.canvas.releasePointerCapture(event.pointerId);
    if (selectionWidth >= MINIMUM_SELECTION_WIDTH) {
      [this.viewMinimum, this.viewMaximum] = logarithmicRangeForSelection(
        this.viewMinimum,
        this.viewMaximum,
        (selection.startX - area.x) / area.width,
        (selection.currentX - area.x) / area.width,
      );
      if (this.resetButton !== null) this.resetButton.hidden = false;
    }
    event.preventDefault();
    this.draw();
  };

  private readonly handlePointerCancel = (event: PointerEvent): void => {
    if (this.dragSelection?.pointerID !== event.pointerId) return;
    this.dragSelection = null;
    this.canvas.classList.remove('is-selecting');
    this.draw();
  };

  private readonly handleReset = (): void => {
    this.dragSelection = null;
    this.viewMinimum = this.fullMinimum;
    this.viewMaximum = this.fullMaximum;
    if (this.resetButton !== null) this.resetButton.hidden = true;
    this.draw();
  };

  private drawAxes(area: PlotArea, minimum: number, maximum: number): void {
    const text = cssColor(this.canvas, '--secondary', '#a3a3a3');
    const divider = cssColor(this.canvas, '--divider', 'rgba(148, 163, 184, 0.18)');
    this.context.font = '12px system-ui, sans-serif';
    this.context.lineWidth = 1;
    this.context.strokeStyle = divider;
    this.context.fillStyle = text;

    for (let index = 0; index <= 4; index += 1) {
      const ratio = index / 4;
      const y = area.y + area.height * (1 - ratio);
      this.context.beginPath();
      this.context.moveTo(area.x, y);
      this.context.lineTo(area.x + area.width, y);
      this.context.stroke();
    }

    for (let index = 0; index <= 4; index += 1) {
      const ratio = index / 4;
      const value = minimum * Math.pow(maximum / minimum, ratio);
      const x = area.x + area.width * ratio;
      this.context.beginPath();
      this.context.moveTo(x, area.y);
      this.context.lineTo(x, area.y + area.height);
      this.context.stroke();
      this.context.textAlign = index === 0 ? 'left' : index === 4 ? 'right' : 'center';
      this.context.textBaseline = 'top';
      this.context.fillText(formatLatency(value), x, area.y + area.height + 10);
    }

    this.context.textAlign = 'center';
    this.context.textBaseline = 'bottom';
    this.context.fillText('Latency (log scale)', area.x + area.width / 2, area.y + area.height + 52);
    this.context.save();
    this.context.translate(18, area.y + area.height / 2);
    this.context.rotate(-Math.PI / 2);
    this.context.fillText('Probability density', 0, 0);
    this.context.restore();
  }

  private drawSeries(
    points: { x: number; density: number }[],
    area: PlotArea,
    minimum: number,
    maximum: number,
    maximumDensity: number,
    color: string,
    fillColor: string,
  ): void {
    if (points.length === 0) return;
    const logMinimum = Math.log(minimum);
    const logRange = Math.log(maximum) - logMinimum || 1;
    const xFor = (value: number): number => area.x + area.width * (Math.log(Math.max(value, minimum)) - logMinimum) / logRange;
    const yFor = (density: number): number => area.y + area.height * (1 - density / (maximumDensity || 1));

    this.context.save();
    this.context.beginPath();
    this.context.rect(area.x, area.y, area.width, area.height);
    this.context.clip();

    if (fillColor !== '') {
      const baseline = area.y + area.height;
      this.context.beginPath();
      this.context.moveTo(xFor(points[0].x), baseline);
      points.forEach((point) => {
        this.context.lineTo(xFor(point.x), yFor(point.density));
      });
      this.context.lineTo(xFor(points[points.length - 1].x), baseline);
      this.context.closePath();
      this.context.fillStyle = fillColor;
      this.context.fill();
    }

    this.context.beginPath();
    points.forEach((point, index) => {
      const x = xFor(point.x);
      const y = yFor(point.density);
      if (index === 0) this.context.moveTo(x, y);
      else this.context.lineTo(x, y);
    });
    this.context.strokeStyle = color;
    this.context.lineWidth = 1.5;
    this.context.lineJoin = 'round';
    this.context.stroke();
    this.context.restore();
  }
}

export function renderAlertPDFComparison(
  canvasID: string,
  comparison: AlertCDFComparison,
  resetButtonID = 'alert-pdf-reset',
): () => void {
  const canvas = document.querySelector<HTMLCanvasElement>(`#${canvasID}`);
  if (canvas === null) return () => { void canvas; };
  const resetButton = document.querySelector<HTMLButtonElement>(`#${resetButtonID}`);
  const plot = new AlertPDFPlot(canvas, comparison, resetButton);
  return () => { plot.destroy(); };
}
