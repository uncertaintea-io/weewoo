export interface Func {
  eval: (x: number) => number
  deriv: () => Func
}

export class ConstFunc implements Func {
  value: number

  constructor(value: number) {
    this.value = value
  }

  eval(x: number): number {
    void x
    return this.value
  }

  deriv(): Func {
    return new ConstFunc(0)
  }
}

export class PolyFunc implements Func {
  coef: number[]

  constructor(coef: number[]) {
    if (coef.length === 0) {
      throw new Error("Can't create an empty polynomial.")
    }
    this.coef = coef
  }

  eval(x: number): number {
    let v = this.coef[0]
    const n = this.coef.length
    for (let i = 1; i < n; i++) {
      v *= x
      v += this.coef[i]
    }
    return v
  }

  deriv(): Func {
    const n = this.coef.length - 1
    if (n === 0) {
      return new ConstFunc(0)
    }
    const newcoef = new Array<number>(n)
    for (let i = 0; i < n; i++) {
      newcoef[i] = (n - i) * this.coef[i]
    }
    return new PolyFunc(newcoef)
  }
}

export function linearFunction(x1: number, y1: number, x2: number, y2: number): PolyFunc {
  const M = (y2 - y1) / (x2 - x1)
  const B = y1 - M * x1
  return new PolyFunc([M, B])
}

function solveCubic(x1: number, y1: number, dy1: number,
  x2: number, y2: number, dy2: number): number[] {
  const dx = x2 - x1
  const xx1 = x1 * x1
  const xx2 = x2 * x2

  let a1 = (xx2 * x2) - (xx1 * x1)
  let a2 = a1
  a1 -= 3 * xx1 * dx
  a2 -= 3 * xx2 * dx

  let b1 = xx2 - xx1
  let b2 = b1
  b1 -= (2 * x1 * dx)
  b2 -= (2 * x2 * dx)

  let e1 = y2 - y1
  let e2 = e1
  e1 -= dy1 * dx
  e2 -= dy2 * dx

  const f = b1 / b2

  const A = (e1 - f * e2) / (a1 - f * a2)
  const B = (e1 - A * a1) / b1
  const C = dy1 - (3 * A * xx1) - (2 * B * x1)
  const D = y1 - (A * xx1 * x1) - (B * xx1) - (C * x1)

  return [A, B, C, D]
}

export function cubicFunction(x1: number, y1: number, dy1: number,
  x2: number, y2: number, dy2: number): PolyFunc {
  return new PolyFunc(
    solveCubic(x1, y1, dy1, x2, y2, dy2))
}

// TODO: Figure out if it's worth it to express Nomalize as a composite function.
// d/dx(f(g(x))) = g'(x) f'(g(x))
// d/dx(f(x) g(x)) = g(x) f'(x) + f(x) g'(x)

export class Normalize {
  private x: number;
  private y: number;
  private dx: number;
  private dy: number;

  constructor(x1: number, y1: number, x2: number, y2: number) {
    this.x = x1
    this.y = y1
    this.dx = x2 - x1
    this.dy = y2 - y1
  }

  x_into(x: number): number {
    return (x - this.x) / this.dx
  }

  x_outof(x: number): number {
    return x * this.dx + this.x
  }

  y_into(y: number): number {
    return (y - this.y) / this.dy
  }

  y_outof(y: number): number {
    return y * this.dy + this.y
  }

  slope_into(m: number): number {
    return m * this.dx / this.dy
  }

  slope_outof(m: number): number {
    return m * this.dy / this.dx
  }
}

class Quint implements Func {
  constructor(private norm: Normalize, private f: Func) {
  }

  eval(x: number): number {
    return this.norm.y_outof(this.f.eval(this.norm.x_into(x)))
  }

  deriv(): Func {
    return new QuintDeriv(this.norm, this.f.deriv())
  }
}

class QuintDeriv implements Func {
  constructor(private norm: Normalize, private f: Func) {
  }

  eval(x: number): number {
    return this.norm.slope_outof(this.f.eval(this.norm.x_into(x)))
  }

  deriv(): Func {
    throw new Error("Not implemented!")
  }
}

export function interpolateMonotonic(xs: number[], ys: number[]): Func[] {
  // Special cases for when we don't have enough data.
  const n = xs.length
  if (n === 0) {
    return [new ConstFunc(0)]
  }
  if (n === 1) {
    return [new ConstFunc(ys[0])]
  }

  // Create a normalized coordinate system between each set of points.
  const ns = new Array<Normalize | null>(n - 1)
  for (let i = 0; i < n - 1; i++) {
    if (ys[i] === ys[i + 1]) {
      ns[i] = null
      continue
    }
    ns[i] = new Normalize(xs[i], ys[i], xs[i + 1], ys[i + 1])
  }

  // Do a first pass, calculating the slopes from the left.
  const ms = new Array<number>(n)
  ms[0] = 0
  for (let i = 1; i < n - 1; i++) {
    const norm = ns[i - 1]
    if (norm === null) {
      ms[i - 1] = 0
      ms[i] = 0
      continue
    }
    let m0 = norm.slope_into(ms[i - 1])
    if (m0 < 0.0) {
      m0 = 0.0
      ms[i - 1] = norm.slope_outof(m0)
    } else if (m0 > 2.0) {
      m0 = 2.0
      ms[i - 1] = norm.slope_outof(m0)
    }
    const m1 = 2.0 - m0
    ms[i] = norm.slope_outof(m1)
    // console.log(`i=${i} ms[i-1]=${ms[i - 1]} m0=${m0} m1=${m1} ms[i]=${ms[i]}`)
  }
  ms[n - 1] = 0

  // Do a second pass, adjusting the slopes from the right.
  for (let i = n - 2; i > 0; i--) {
    const norm = ns[i]
    if (norm === null) {
      continue
    }
    let m1 = norm.slope_into(ms[i + 1])
    if (m1 < 0.0) {
      m1 = 0.0
      ms[i + 1] = norm.slope_outof(m1)
    } else if (m1 > 2.0) {
      m1 = 2.0
      ms[i + 1] = norm.slope_outof(m1)
    }
    const m0 = 2.0 - m1
    const out = norm.slope_outof(m0)
    if (out < ms[i]) {
      ms[i] = out
    }
    // console.log(`i=${i} ms[i]=${ms[i]} m0=${m0} m1=${m1} ms[i+1]=${ms[i + 1]}`)
  }

  // Create the functions.
  const fs = new Array<Func>(n - 1)
  for (let i = 0; i < n - 1; i++) {
    const norm = ns[i]
    if (norm === null) {
      fs[i] = new ConstFunc(ys[i])
      continue
    }
    const m0 = norm.slope_into(ms[i])
    const m1 = norm.slope_into(ms[i + 1])
    const A = 6 - 3 * m0 - 3 * m1
    const B = -15 + 8 * m0 + 7 * m1
    const C = 10 - 6 * m0 - 4 * m1
    const E = m0
    fs[i] = new Quint(norm, new PolyFunc([A, B, C, 0, E, 0]))
  }
  return fs
}

/*
FritschCarlsonTangents calculates tangents for a set of points
that ensure monotonicity for a resulting Hermite spline.

This function makes some important assumptions:
  1. That the input arrays have the same length.
  2. That the data points are monotonic.
  3. That the input points are sorted on the x axis, ascending.
These assumptions are not verified by the method.
*/
export function FritschCarlsonTangents(xs: number[], ys: number[]): number[] {
  // For implementation details, see:
  // https://en.wikipedia.org/wiki/Monotone_cubic_interpolation
  const n = xs.length
  if (n === 0) {
    return []
  }
  if (n === 1) {
    return [ys[0]]
  }
  // Compute the slopes of the secant lines between successive points
  const d = new Array<number>(n - 1)
  for (let i = 0; i < n - 1; i++) {
    d[i] = (ys[i + 1] - ys[i]) / (xs[i + 1] - xs[i])
  }
  // Compute provisional tangents
  const m = new Array<number>(n)
  m[0] = d[0]
  m[n - 1] = d[n - 2]
  for (let i = 1; i < n - 1; i++) {
    if (d[i] === 0.0) {
      m[i] = 0.0
      i += 1
      m[i] = 0.0
      continue
    }
    if (Math.sign(d[i - 1]) !== Math.sign(d[i])) {
      m[i] = 0.0
    } else {
      m[i] = (d[i - 1] + d[i]) / 2
    }
  }
  // Adjust tangents to keep monoticity.
  for (let i = 0; i < n - 1; i++) {
    const dk = d[i]
    const ak = m[i] / dk
    const bk = m[i + 1] / dk
    const sqsum = ak * ak + bk * bk
    if (sqsum > 9.0) {
      const tk = 3.0 / Math.sqrt(sqsum)
      m[i] = tk * ak * dk
      m[i + 1] = tk * bk * dk
    }
  }
  return m
}
