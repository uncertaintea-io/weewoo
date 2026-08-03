import { type RGBA } from './colormap'
import { type Func, ConstFunc, linearFunction, cubicFunction } from './func'

interface SegmentList {
  x: number
  f: Func
  next: SegmentList | null
}

export function startSegment(): SegmentList {
  return {
    x: Number.NEGATIVE_INFINITY,
    f: new ConstFunc(0),
    next: null
  }
}

export function findFrontTail(x1: number, y1: number, dy1: number): SegmentList {
  const x0 = x1 - 2 * y1 / dy1
  return {
    x: x0,
    f: cubicFunction(x0, 0, 0, x1, y1, dy1),
    next: null
  }
}

export function findBackTail(x1: number, y1: number, dy1: number): SegmentList {
  const x2 = 2 * (1 - y1) / dy1 + x1
  return {
    x: x1,
    f: cubicFunction(x1, y1, dy1, x2, 1, 0),
    next: {
      x: x2,
      f: new ConstFunc(1),
      next: null
    }
  }
}

export interface Plot {
  segments: SegmentList
  minX: number
  maxX: number
}

// A function that, given an image mapping, generates min, max, array of y values,
// and whether it is monotonic or not.
type RenderFunc = (w: number, cx2fx: Func) => [number, number, number[], boolean]

export function getRenderFunc(p: Plot): RenderFunc {
  return function (w: number, cx2fx: Func): [number, number, number[], boolean] {
    let minY = Number.POSITIVE_INFINITY
    let maxY = Number.NEGATIVE_INFINITY

    // For each X in the image, find the Y value.
    const ys = new Array<number>(w)
    let monotonic = true
    let lastY: number | undefined = undefined
    let s = p.segments
    for (let cx = 0; cx < w; cx++) {
      // What is the value of x in function space?
      const fx = cx2fx.eval(cx)
      // Have we started the next segment?
      while (s.next != null && fx >= s.next.x) {
        s = s.next
        lastY = undefined
      }
      // Calculate the value of y in function space.
      const fy = s.f.eval(fx)
      if (fy < minY) minY = fy
      if (fy > maxY) maxY = fy
      ys[cx] = fy
      if (monotonic) {
        if (lastY !== undefined) {
          monotonic = fy >= lastY
        }
        lastY = fy
      }
    }
    return [minY, maxY, ys, monotonic]
  }
}

export class Layer {
  public ys: number[]
  public monotonic = true
  public image: ImageData | null = null

  constructor(private readonly f: RenderFunc, private readonly lineColor: RGBA, private readonly fillColor: RGBA) {
    this.ys = []
  }

  public scan(w: number, cx2fx: Func): [number, number] {
    // For each X in the image, find the Y value.
    const [min, max, ys, monotonic] = this.f(w, cx2fx)
    // console.log('ymin:', min)
    // console.log('ymax:', max)
    // console.log('ys:', ys)
    this.ys = ys
    this.monotonic = monotonic
    return [min, max]
  }

  public render(w: number, h: number, cx2fx: Func, fy2cy: Func): ImageData {
    // Convert y values to coordinate space
    //console.log('ys before conversion:', this.ys)

    // Special featue for this app:
    // turn the curve red if the graph is not monotonic
    let fillColor, lineColor
    if (this.monotonic) {
      fillColor = this.fillColor
      lineColor = this.lineColor
    } else {
      fillColor = {
        r: this.fillColor.b,
        g: this.fillColor.g,
        b: this.fillColor.r,
        a: this.fillColor.a,
      }
      lineColor = {
        r: this.lineColor.b,
        g: this.lineColor.g,
        b: this.lineColor.r,
        a: this.lineColor.a,
      }
    }

    const ys = this.ys.map(fy => fy2cy.eval(fy))
    //console.log('ys after conversion:', ys)
    // Render the fill
    const pixels = new Uint8ClampedArray(w * h * 4)
    for (let cy = 0; cy < h; cy++) {
      let i = cy * w * 4
      for (let cx = 0; cx < w; cx++) {
        if (ys[cx] <= cy) {
          // color under the graph
          pixels[i] = fillColor.r
          i++
          pixels[i] = fillColor.g
          i++
          pixels[i] = fillColor.b
          i++
          pixels[i] = fillColor.a
          i++
        } else {
          // color over the graph
          pixels[i] = 0 // R
          i++
          pixels[i] = 0 // G
          i++
          pixels[i] = 0 // B
          i++
          pixels[i] = 0 // A
          i++
        }
      }
    }
    // Draw the curve itself.
    let lastY = Math.floor(ys[0])
    const limY = h - 1
    for (let cx = 0; cx < w; cx++) {
      // For each x value, find the y values that should be colored in.
      let cy2 = Math.floor(ys[cx])
      let cy1 = lastY
      lastY = cy2
      if (cy1 > cy2) {
        const tmp = cy2
        cy2 = cy1
        cy1 = tmp
      }
      // Make sure the lines are within the bounds of the image
      if (cy2 < 0) continue
      cy1 -= 1
      cy2 += 1
      if (cy1 < 0) cy1 = 0
      if (cy1 > limY) continue
      if (cy2 > limY) cy2 = limY
      // Color in the pixels
      for (; cy1 <= cy2; cy1++) {
        let i = (cy1 * w + cx) * 4
        pixels[i] = lineColor.r
        i++
        pixels[i] = lineColor.g
        i++
        pixels[i] = lineColor.b
        i++
        pixels[i] = lineColor.a
      }
    }
    return new ImageData(pixels, w, h)
  }
}

export class CDFGraph {
  private cx2fx: Func = linearFunction(0, 0, 1, 1)
  //private fx2cx: Func
  private fy2cy: Func = linearFunction(0, 0, 1, 1)
  private cy2fy: Func = linearFunction(0, 0, 1, 1)
  private minX = 0
  public maxX = 1
  private minY = 0
  private maxY = 1
  private layers: Layer[]

  public mouseHook: (x: number, y: number, pressed: boolean) => void

  constructor(private readonly canvas: HTMLCanvasElement) {
    this.layers = []
    this.onResize()
    this.mouseHook = function (x, y, pressed) {
      void x
      void y
      void pressed
    }
  }

  public setLayer(i: number, layer: Layer): void {
    this.layers[i] = layer
    layer.image = null
    this.onResize()
  }

  public onResize(e?: Event): void {
    void e
    const w = this.canvas.width
    const h = this.canvas.height

    // TODO: This shouldn't be hard-coded
    this.minY = 0.0
    this.maxY = 12.0
    this.fy2cy = linearFunction(this.minY, h - 1, this.maxY, 0)
    this.cy2fy = linearFunction(h - 1, this.minY, 0, this.maxY)

    this.minX = 0
    this.maxX = (this.maxY * w) / h
    this.cx2fx = linearFunction(0, this.minX, w, this.maxX)
    //this.fx2cx = linearFunction(this.minX, 0, this.maxX, w)

    // reset all the layers
    for (const layer of this.layers) {
      layer.image = null
      layer.scan(w, this.cx2fx)
    }

    // force a redraw
    this.draw()
  }

  public draw(): void {
    const w = this.canvas.width
    const h = this.canvas.height
    const ctx = this.canvas.getContext('2d')
    if (ctx == null) throw new Error('Unable to create 2d context!')
    ctx.clearRect(0, 0, w, h)
    for (let i = 0; i < this.layers.length; i++) {
      const layer = this.layers[i]
      layer.image ??= layer.render(w, h, this.cx2fx, this.fy2cy)
      if (i === 0) {
        ctx.putImageData(layer.image, 0, 0)
      } else {
        // create a temporary canvas to hold the overlay
        const canvas2 = document.createElement('canvas')
        canvas2.width = w
        canvas2.height = h
        const ctx2 = canvas2.getContext('2d')
        if (ctx2 == null) throw new Error('Unable to create 2d context!')
        ctx2.putImageData(layer.image, 0, 0)
        ctx.drawImage(canvas2, 0, 0)
      }
    }
  }

  public onMouse(e: MouseEvent) {
    const dpr = window.devicePixelRatio;
    const cx = e.offsetX * dpr
    const cy = e.offsetY * dpr
    const fx = this.cx2fx.eval(cx)
    const fy = this.cy2fy.eval(cy)
    this.mouseHook(fx, fy, e.buttons === 1)
    return false
  }
}

export function resizeCanvasToDisplaySize(canvas: HTMLCanvasElement): boolean {
  // Lookup the size the browser is displaying the canvas in CSS pixels.
  const dpr = window.devicePixelRatio
  const displayWidth = Math.floor(canvas.clientWidth * dpr)
  const displayHeight = Math.floor(canvas.clientHeight * dpr)

  // Check if the canvas is the same size as before:
  if (canvas.width === displayWidth && canvas.height === displayHeight) {
    // Nothing to do!
    return false
  }

  // Otherwise, adjust the canvas:
  canvas.width = displayWidth
  canvas.height = displayHeight
  return true
}

export function createGraph(id: string): CDFGraph {
  const canvas = document.getElementById(id) as HTMLCanvasElement
  resizeCanvasToDisplaySize(canvas)
  const graph = new CDFGraph(canvas)
  canvas.addEventListener('resize', function (e) {
    if (resizeCanvasToDisplaySize(canvas)) {
      graph.onResize(e)
    }
  }, false)
  graph.onResize()

  const mouseHandler = function (e: MouseEvent) {
    graph.onMouse(e)
  }
  canvas.addEventListener('mousemove', mouseHandler, false)
  canvas.addEventListener('mousedown', mouseHandler, false)
  canvas.addEventListener('mouseup', mouseHandler, false)

  return graph
}
