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
