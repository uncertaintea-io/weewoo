import { GetJointECDF, ServicesApiError, type JointECDFRender } from './api';
import { viridis } from './colormap';
import { linearFunction, type Func } from './func';

const LOAD_LATENCY_INDICATOR_ID = 1;
const DEFAULT_GAMMA = 0.55;
const PLOT_MARGIN = { top: 18, right: 18, bottom: 64, left: 76 };

interface PlotArea {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface CellSelection {
  column: number;
  row: number;
  load: number;
  latency: number;
  mass: number;
}

export function normalizeMasses(masses: number[], gamma = DEFAULT_GAMMA): number[] {
  const maximum = Math.max(0, ...masses);
  if (maximum === 0) {
    return masses.map(() => 0);
  }
  return masses.map((mass) => Math.pow(mass / maximum, gamma));
}

export function densityPixels(data: JointECDFRender, gamma = DEFAULT_GAMMA): Uint8ClampedArray {
  const normalized = normalizeMasses(data.masses, gamma);
  const pixels = new Uint8ClampedArray(normalized.length * 4);
  normalized.forEach((mass, index) => {
    const color = viridis(mass);
    const offset = index * 4;
    pixels[offset] = color.r;
    pixels[offset + 1] = color.g;
    pixels[offset + 2] = color.b;
    pixels[offset + 3] = color.a;
  });
  return pixels;
}

function formatAxisValue(value: number): string {
  if (value === 0) return '0';
  // const magnitude = Math.abs(value);
  // if (magnitude >= 10000 || magnitude < 0.001) {
  //   return value.toExponential(1);
  // }
  return new Intl.NumberFormat(undefined, { maximumSignificantDigits: 3 }).format(value);
}

export function formatLatencySeconds(seconds: number): string {
  const magnitude = Math.abs(seconds);
  if (magnitude === 0 || magnitude >= 1) {
    return `${formatAxisValue(seconds)}s`;
  }
  if (magnitude >= 0.001) {
    return `${formatAxisValue(seconds * 1000)}ms`;
  }
  if (magnitude >= 0.000001) {
    return `${formatAxisValue(seconds * 1000000)}µs`;
  }
  return `${formatAxisValue(seconds * 1000000000)}ns`;
}

class JointECDFPlot {
  private readonly context: CanvasRenderingContext2D;
  private readonly densityCanvas: HTMLCanvasElement;
  private readonly resizeObserver: ResizeObserver | null;
  private selection: CellSelection | null = null;
  private plotArea: PlotArea | null = null;
  private cx2fx: Func | null = null;
  private cy2fy: Func | null = null;

  public constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly data: JointECDFRender,
    private readonly onSelection: (selection: CellSelection | null) => void,
    gamma = DEFAULT_GAMMA,
  ) {
    const context = canvas.getContext('2d');
    if (context === null) throw new Error('Unable to create the Joint ECDF canvas context.');
    this.context = context;
    this.densityCanvas = document.createElement('canvas');
    this.densityCanvas.width = data.width;
    this.densityCanvas.height = data.height;
    const densityContext = this.densityCanvas.getContext('2d');
    if (densityContext === null) throw new Error('Unable to create the Joint ECDF density image.');
    const densityImage = densityContext.createImageData(data.width, data.height);
    densityImage.data.set(densityPixels(data, gamma));
    densityContext.putImageData(densityImage, 0, 0);

    this.canvas.addEventListener('pointermove', this.handlePointerMove);
    this.canvas.addEventListener('pointerleave', this.handlePointerLeave);
    this.resizeObserver = typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(() => { this.draw(); });
    this.resizeObserver?.observe(canvas);
    window.addEventListener('resize', this.draw);
    this.draw();
  }

  public destroy(): void {
    this.resizeObserver?.disconnect();
    window.removeEventListener('resize', this.draw);
    this.canvas.removeEventListener('pointermove', this.handlePointerMove);
    this.canvas.removeEventListener('pointerleave', this.handlePointerLeave);
  }

  private readonly draw = (): void => {
    const width = this.canvas.clientWidth;
    const height = this.canvas.clientHeight;
    if (width <= 0 || height <= 0) return;

    const dpr = window.devicePixelRatio || 1;
    const physicalWidth = Math.round(width * dpr);
    const physicalHeight = Math.round(height * dpr);
    if (this.canvas.width !== physicalWidth || this.canvas.height !== physicalHeight) {
      this.canvas.width = physicalWidth;
      this.canvas.height = physicalHeight;
    }

    const context = this.context;
    context.setTransform(dpr, 0, 0, dpr, 0, 0);
    context.clearRect(0, 0, width, height);

    const availableWidth = Math.max(1, width - PLOT_MARGIN.left - PLOT_MARGIN.right);
    const availableHeight = Math.max(1, height - PLOT_MARGIN.top - PLOT_MARGIN.bottom);
    const side = Math.min(availableWidth, availableHeight);
    const area: PlotArea = {
      x: PLOT_MARGIN.left + (availableWidth - side) / 2,
      y: PLOT_MARGIN.top,
      width: side,
      height: side,
    };
    this.plotArea = area;
    this.cx2fx = linearFunction(area.x, this.data.xMin, area.x + area.width, this.data.xMax);
    this.cy2fy = linearFunction(area.y, this.data.yMax, area.y + area.height, this.data.yMin);

    context.save();
    context.imageSmoothingEnabled = false;
    context.drawImage(this.densityCanvas, area.x, area.y, area.width, area.height);
    context.restore();
    this.drawAxes(area);
    this.drawSelection(area);
  };

  private drawAxes(area: PlotArea): void {
    const context = this.context;
    const styles = getComputedStyle(document.documentElement);
    const textColor = styles.getPropertyValue('--secondary').trim() || '#bcc7dc';
    const lineColor = styles.getPropertyValue('--border').trim() || 'rgba(172, 188, 224, 0.35)';
    const ticks = [0, 0.5, 1];

    context.save();
    context.strokeStyle = lineColor;
    context.fillStyle = textColor;
    context.lineWidth = 1;
    context.font = '12px Inter, ui-sans-serif, system-ui, sans-serif';
    context.beginPath();
    context.rect(area.x - 0.5, area.y - 0.5, area.width + 1, area.height + 1);
    context.stroke();

    context.textAlign = 'center';
    context.textBaseline = 'top';
    ticks.forEach((tick) => {
      const x = area.x + tick * area.width;
      const value = this.data.xMin + tick * (this.data.xMax - this.data.xMin);
      context.beginPath();
      context.moveTo(x, area.y + area.height);
      context.lineTo(x, area.y + area.height + 6);
      context.stroke();
      context.fillText(formatAxisValue(value), x, area.y + area.height + 10);
    });
    context.font = '600 13px Inter, ui-sans-serif, system-ui, sans-serif';
    context.fillText('Load', area.x + area.width / 2, area.y + area.height + 36);

    context.font = '12px Inter, ui-sans-serif, system-ui, sans-serif';
    context.textAlign = 'right';
    context.textBaseline = 'middle';
    ticks.forEach((tick) => {
      const y = area.y + tick * area.height;
      const value = this.data.yMax - tick * (this.data.yMax - this.data.yMin);
      context.beginPath();
      context.moveTo(area.x - 6, y);
      context.lineTo(area.x, y);
      context.stroke();
      context.fillText(formatLatencySeconds(value), area.x - 10, y);
    });
    context.translate(18, area.y + area.height / 2);
    context.rotate(-Math.PI / 2);
    context.font = '600 13px Inter, ui-sans-serif, system-ui, sans-serif';
    context.textAlign = 'center';
    context.fillText('Latency', 0, 0);
    context.restore();
  }

  private drawSelection(area: PlotArea): void {
    if (this.selection === null) return;
    const cellWidth = area.width / this.data.width;
    const cellHeight = area.height / this.data.height;
    const x = area.x + this.selection.column * cellWidth;
    const y = area.y + this.selection.row * cellHeight;

    this.context.save();
    this.context.strokeStyle = '#ffffff';
    this.context.lineWidth = 1.5;
    this.context.shadowColor = 'rgba(0, 0, 0, 0.75)';
    this.context.shadowBlur = 3;
    this.context.strokeRect(x + 0.75, y + 0.75, Math.max(1, cellWidth - 1.5), Math.max(1, cellHeight - 1.5));
    this.context.restore();
  }

  private readonly handlePointerMove = (event: PointerEvent): void => {
    const area = this.plotArea;
    if (area === null || this.cx2fx === null || this.cy2fy === null) return;
    const bounds = this.canvas.getBoundingClientRect();
    const x = event.clientX - bounds.left;
    const y = event.clientY - bounds.top;
    if (x < area.x || x >= area.x + area.width || y < area.y || y >= area.y + area.height) {
      this.clearSelection();
      return;
    }

    const column = Math.min(this.data.width - 1, Math.floor((x - area.x) / area.width * this.data.width));
    const row = Math.min(this.data.height - 1, Math.floor((y - area.y) / area.height * this.data.height));
    const selection: CellSelection = {
      column,
      row,
      load: this.cx2fx.eval(area.x + (column + 0.5) / this.data.width * area.width),
      latency: this.cy2fy.eval(area.y + (row + 0.5) / this.data.height * area.height),
      mass: this.data.masses[row * this.data.width + column] ?? 0,
    };
    this.selection = selection;
    this.onSelection(selection);
    this.draw();
  };

  private readonly handlePointerLeave = (): void => {
    this.clearSelection();
  };

  private clearSelection(): void {
    if (this.selection === null) return;
    this.selection = null;
    this.onSelection(null);
    this.draw();
  }
}

function selectionText(selection: CellSelection): string {
  return `Load: ${formatAxisValue(selection.load)} · Latency: ${formatLatencySeconds(selection.latency)}`;
  // · Cell mass: ${new Intl.NumberFormat(undefined, { maximumSignificantDigits: 3 }).format(selection.mass)}
}

// Take ownership of a canvas and create a visualization of a service's Load vs. Latency Joint ECDF.
export function renderJECDF(id: string, serviceID: number): () => void {
  const canvas = document.getElementById(id);
  const status = document.getElementById(`${id}-status`);
  if (!(canvas instanceof HTMLCanvasElement)) {
    throw new Error(`Joint ECDF canvas "${id}" was not found.`);
  }

  const controller = new AbortController();
  let plot: JointECDFPlot | null = null;
  void GetJointECDF(serviceID, LOAD_LATENCY_INDICATOR_ID, (input, init) => (
    fetch(input, { ...init, signal: controller.signal })
  )).then((data) => {
    if (controller.signal.aborted) return;
    canvas.classList.remove('is-loading');
    plot = new JointECDFPlot(canvas, data, (selection) => {
      if (status !== null) {
        status.textContent = selection === null
          ? 'Hover over the plot to inspect a cell.'
          : selectionText(selection);
      }
    });
    if (status !== null) status.textContent = 'Hover over the plot to inspect a cell.';
  }).catch((error: unknown) => {
    if (controller.signal.aborted) return;
    canvas.hidden = true;
    if (status !== null) {
      status.classList.add('is-error');
      status.textContent = error instanceof ServicesApiError && error.status === 404
        ? 'The Load vs. Latency baseline is not available yet.'
        : error instanceof Error ? error.message : 'Unable to load the Joint ECDF.';
    }
  });

  return () => {
    controller.abort();
    plot?.destroy();
  };
}
