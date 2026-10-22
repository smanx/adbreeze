// 投屏渲染器：移植自 ya-webadb（Tango）的 scrcpy-decoder 渲染层。
//
// 吸收原方案四点优势（此前是直接 ctx.drawImage 到固定尺寸画布）：
//   1. WebGL 上屏：VideoFrame 直接作为纹理源，零 CPU 拷贝；不支持硬件 WebGL
//      时降级 ImageBitmapRenderingContext / 2D canvas；context lost 自动重建。
//   2. 画布内部分辨率按显示尺寸封顶：窗口小于编码分辨率时不再浪费像素，
//      缩放交给着色器（双线性 / Mitchell-Netravali / tent4），
//      避免浏览器 drawImage 默认的 bilinear 毛刺。
//   3. rAF 帧门控：渲染跟不上解码时只保留最新一帧（延迟优先），
//      并统计 rendered / displayed / skipped。
//   4. ResizeObserver 驱动重绘：窗口/全屏变化时用最后一帧立即重采样。
//
// 与 Tango 的差异：画布仍是页面里那个可聚焦、承载鼠标/触摸事件的
// #mirror-canvas（Tango 自建离屏画布），因此内部尺寸只在「等比缩放」前提下
// 变化，避免改变 CSS 盒子尺寸触发 ResizeObserver 回环。

export type RenderKind = 'webgl' | 'canvas2d'

/** WASM 软解输出的 YUV420p 平面（bytes 是 WASM 堆视图，只能同步读取） */
export interface YUVFrame {
  y: Uint8Array
  u: Uint8Array
  v: Uint8Array
  /** 亮度平面行字节数 */
  stride: number
  chromaStride: number
  lumaWidth: number
  lumaHeight: number
  /** 可见区域（裁剪后） */
  cropLeft: number
  cropTop: number
  cropWidth: number
  cropHeight: number
  limitedRange: boolean
  bt709: boolean
}

const VERT = `
attribute vec2 xy;
varying highp vec2 uv;
void main(void) {
  gl_Position = vec4(xy, 0.0, 1.0);
  uv = xy * 0.5 + 0.5;
  uv.y = 1.0 - uv.y;
}`

/** RGBA 单纹理采样：按缩放比选 双线性 / Mitchell / 4 抽头 tent */
const FRAG_RGBA = `
precision mediump float;
uniform sampler2D source;
uniform vec2 texelSize;
uniform float zoom;
uniform mat2 uM;
uniform vec2 uT;
varying vec2 uv;

vec4 tent4(vec2 p) {
  vec2 dx = vec2(texelSize.x, 0.0);
  vec2 dy = vec2(0.0, texelSize.y);
  vec4 c0 = texture2D(source, p);
  vec4 c1 = texture2D(source, p + dx);
  vec4 c2 = texture2D(source, p + dy);
  vec4 c3 = texture2D(source, p + dx + dy);
  return 0.25 * (c0 + c1 + c2 + c3);
}

float mnWeight(float x) {
  x = abs(x);
  float x2 = x * x;
  float x3 = x2 * x;
  const float s = 1.0 / 3.0;
  if (x < 1.0) {
    return (1.0 / 6.0) * ((12.0 - 9.0 * s - 6.0 * s) * x3 +
                          (-18.0 + 12.0 * s + 6.0 * s) * x2 +
                          (6.0 - 2.0 * s));
  } else if (x < 2.0) {
    return (1.0 / 6.0) * ((-s - 6.0 * s) * x3 +
                          ((6.0 * s + 30.0 * s) * x2 +
                           (-12.0 * s - 48.0 * s) * x +
                           (8.0 * s + 24.0 * s)));
  }
  return 0.0;
}

vec4 bicubicMN(vec2 p) {
  vec2 texCoord = p / texelSize;
  vec2 base = floor(texCoord - 0.5);
  vec2 f = texCoord - base - 0.5;
  vec4 sum = vec4(0.0);
  float total = 0.0;
  for (int j = -1; j <= 2; j++) {
    float wy = mnWeight(float(j) - f.y);
    for (int i = -1; i <= 2; i++) {
      float wx = mnWeight(float(i) - f.x);
      vec2 coord = (base + vec2(float(i), float(j)) + 0.5) * texelSize;
      sum += texture2D(source, coord) * (wx * wy);
      total += wx * wy;
    }
  }
  return sum / total;
}

void main() {
  // 视图旋转在采样端完成：目标像素 uv 映射回未旋转的源纹理坐标
  vec2 p = uM * uv + uT;
  if (zoom > 0.95) {
    gl_FragColor = texture2D(source, p);
  } else if (zoom > 0.5) {
    gl_FragColor = bicubicMN(p);
  } else {
    gl_FragColor = tent4(p);
  }
}`

/** YUV420p 三纹理：着色器内做 BT.601/BT.709 与有限/全范围转换 */
const FRAG_YUV = `
precision mediump float;
uniform sampler2D yPlane;
uniform sampler2D uPlane;
uniform sampler2D vPlane;
uniform vec2 lumaSize;
uniform vec2 chromaSize;
uniform vec3 kY;
uniform vec3 kc;
uniform mat3 ccm;
uniform mat2 uM;
uniform vec2 uT;
varying vec2 uv;

void main() {
  vec2 p = uM * uv + uT;
  float y = texture2D(yPlane, p).r * kY.x + kY.y;
  // 4:2:0 同位（co-sited）色度：先把亮度像素中心折半，再回到色度纹理的像素中心
  vec2 c = (p * lumaSize - vec2(0.5, 0.5)) * 0.5 + vec2(0.5, 0.5);
  vec2 cu = c / chromaSize;
  float u = texture2D(uPlane, cu).r * kc.x + kc.y;
  float v = texture2D(vPlane, cu).r * kc.x + kc.y;
  gl_FragColor = vec4(clamp(ccm * vec3(y, u, v), 0.0, 1.0), 1.0);
}`

// YUV -> RGB 矩阵（按行主序书写，传入时转置为列主序）。
// 约定：着色器里 y/u/v 只做了「减偏移」（kY/kc 的 x 分量恒为 1），
// 亮度增益与色度系数全部落在矩阵里，避免重复计入。
// 有限范围对角线 1.164（= 255/219），全范围对角线 1.0。
// prettier-ignore
const CCM_601_LIMITED = [1.164383, 0.0, 1.596027, 1.164383, -0.391762, -0.812968, 1.164383, 2.017232, 0.0];
// prettier-ignore
const CCM_709_LIMITED = [1.164383, 0.0, 1.792741, 1.164383, -0.213248, -0.532909, 1.164383, 2.112401, 0.0];
// prettier-ignore
const CCM_601_FULL = [1.0, 0.0, 1.402, 1.0, -0.344136, -0.714136, 1.0, 1.772, 0.0];
// prettier-ignore
const CCM_709_FULL = [1.0, 0.0, 1.7924, 1.0, -0.213248, -0.532909, 1.0, 2.112401, 0.0];

/** 有限范围：Y'∈[16,235]、C∈[16,240]（色度中心 128）；全范围：[0,255]（中心 128=0.5） */
function yuvOffsets(limitedRange: boolean) {
  return {
    // kY = (scale, offset)：y' = y*scale + offset。此处 scale 恒 1，只减黑电平。
    yOff: limitedRange ? -16 / 255 : 0,
    // 色度减中心：有限/全范围都以 128 为中心（Cb/Cr=128 表示无色度）
    cOff: -128 / 255,
  }
}

// 覆盖整个视口的单个超大三角形（比两个三角形少一次对角线拼接）
const VERTICES = new Float32Array([-1, -1, 3, -1, -1, 3])

function shader(gl: WebGLRenderingContext, type: number, src: string) {
  const s = gl.createShader(type)!
  gl.shaderSource(s, src)
  gl.compileShader(s)
  return s
}

function program(gl: WebGLRenderingContext, vs: string, fs: string) {
  const v = shader(gl, gl.VERTEX_SHADER, vs)
  const f = shader(gl, gl.FRAGMENT_SHADER, fs)
  const p = gl.createProgram()!
  gl.attachShader(p, v)
  gl.attachShader(p, f)
  gl.linkProgram(p)
  try {
    if (gl.getProgramParameter(p, gl.LINK_STATUS)) return p
    if (!gl.getShaderParameter(v, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(v) || '')
    if (!gl.getShaderParameter(f, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(f) || '')
    throw new Error(gl.getProgramInfoLog(p) || '')
  } finally {
    gl.deleteShader(v)
    gl.deleteShader(f)
  }
}

function probeCanvas(): HTMLCanvasElement | OffscreenCanvas {
  if (typeof document !== 'undefined') return document.createElement('canvas')
  return new OffscreenCanvas(1, 1)
}

function createGL(canvas: HTMLCanvasElement, attrs: WebGLContextAttributes): WebGL2RenderingContext | null {
  // 只接受 WebGL2：YUV 纹理上传依赖 UNPACK_ROW_LENGTH（WebGL1 没有），
  // 若允许 WebGL1，软解会退到需要 2D 上下文的 CPU 路径——而同一画布已经
  // 绑定了 WebGL 上下文，拿不到 2D，结果是「解码成功但一帧都不画」。
  // WebGL1-only 的浏览器直接走 2D 渲染器，功能完整只是少一个缩放着色器。
  try {
    return canvas.getContext('webgl2', attrs) as WebGL2RenderingContext | null
  } catch {
    return null
  }
}

/** 是否允许硬件 WebGL2（禁用软件光栅器：软件 WebGL 比 2D 更慢） */
export function webglHardwareSupported() {
  const canvas = probeCanvas()
  if (!(canvas instanceof HTMLCanvasElement)) return false
  const gl = createGL(canvas, { failIfMajorPerformanceCaveat: true })
  if (gl) {
    try { gl.getExtension('WEBGL_lose_context')?.loseContext() } catch { /* 忽略 */ }
  }
  return !!gl
}

export interface RenderCounters {
  rendered: number
  displayed: number
  skipped: number
}

/** 等待 rAF 的绘制任务（一次只保留最新一帧） */
type Pending =
  | { kind: 'rgba'; frame: VideoFrame }
  | { kind: 'yuv'; width: number; height: number; limitedRange: boolean; bt709: boolean }

/**
 * 视频帧渲染器。调用方只需 render()/renderYUV()/renderVideo()，
 * 画布内部分辨率、显示尺寸、缩放滤波、丢帧与重绘全部由内部处理。
 *
 * 尺寸策略：观察 stage（由 flex 布局确定，与画布无关）得到可用空间，
 * 再把「视频宽高比（含视图旋转）」等比 fit 进去，显式写回画布的 CSS 尺寸
 * 与内部分辨率。不依赖画布自身盒子，避免「盒子←属性尺寸←盒子」的循环，
 * 因此画面能真正撑满可用区域而不变形。
 */
export class FrameRenderer {
  private canvas: HTMLCanvasElement
  private stage: HTMLElement
  private kind: RenderKind
  private gl: WebGL2RenderingContext | null = null
  private ctx2d: CanvasRenderingContext2D | null = null

  private blitProg!: WebGLProgram
  private yuvProg!: WebGLProgram
  private vbo!: WebGLBuffer
  private texRGBA!: WebGLTexture
  private texY!: WebGLTexture
  private texU!: WebGLTexture
  private texV!: WebGLTexture
  private blitLoc!: { source: WebGLUniformLocation | null; texelSize: WebGLUniformLocation | null; zoom: WebGLUniformLocation | null; uM: WebGLUniformLocation | null; uT: WebGLUniformLocation | null }
  private yuvLoc!: {
    yPlane: WebGLUniformLocation | null
    uPlane: WebGLUniformLocation | null
    vPlane: WebGLUniformLocation | null
    lumaSize: WebGLUniformLocation | null
    chromaSize: WebGLUniformLocation | null
    kY: WebGLUniformLocation | null
    kc: WebGLUniformLocation | null
    ccm: WebGLUniformLocation | null
    uM: WebGLUniformLocation | null
    uT: WebGLUniformLocation | null
  }

  private closed = false
  private paused = false

  private pending: Pending | null = null
  private lastRGBA: VideoFrame | null = null
  private lastYUV: { width: number; height: number; limitedRange: boolean; bt709: boolean } | null = null
  private raf = 0

  private counters: RenderCounters = { rendered: 0, displayed: 0, skipped: 0 }
  private availW = 0
  private availH = 0
  private codedW = 0
  private codedH = 0
  private ro: ResizeObserver | null = null
  /** WebGL 上下文彻底无法恢复时的通知（画布上下文类型不可切换，只能上报） */
  private onUnrecoverable: (() => void) | undefined
  /** 视图旋转（仅影响网页显示与触控映射，不旋转设备） */
  private rotation = 0

  constructor(canvas: HTMLCanvasElement, opts?: { stage?: HTMLElement; preferWebGL?: boolean; onUnrecoverable?: () => void }) {
    this.canvas = canvas
    // 观察舞台而非画布：舞台尺寸由外层 flex 布局确定，与画布无关，
    // 因此不存在「画布盒子←属性尺寸←盒子」的循环，画面才能撑满可用区域。
    this.stage = opts?.stage ?? canvas.parentElement ?? canvas
    this.onUnrecoverable = opts?.onUnrecoverable
    if (opts?.preferWebGL !== false && webglHardwareSupported()) {
      const created = createGL(canvas, {
        alpha: true,
        antialias: false,
        depth: false,
        stencil: false,
        premultipliedAlpha: true,
        powerPreference: 'low-power',
        desynchronized: true,
        preserveDrawingBuffer: false,
        failIfMajorPerformanceCaveat: true,
      })
      if (created) {
        this.gl = created
        this.kind = 'webgl'
        this.initGL()
        canvas.addEventListener('webglcontextlost', this.onLost)
        canvas.addEventListener('webglcontextrestored', this.onRestored)
      } else {
        this.kind = 'canvas2d'
      }
    } else {
      this.kind = 'canvas2d'
    }

    if (this.kind !== 'webgl') {
      this.ctx2d = canvas.getContext('2d')
      this.kind = 'canvas2d'
    }

    if (typeof ResizeObserver !== 'undefined') {
      this.ro = new ResizeObserver(() => this.measureStage())
      this.ro.observe(this.stage)
    }
    this.measureStage()
  }

  /** 读取舞台内容区尺寸（物理像素），作为画面可用空间 */
  private measureStage() {
    const s = this.stage
    let w = 0, h = 0
    if (s === this.canvas) {
      w = s.clientWidth; h = s.clientHeight
    } else {
      const cs = getComputedStyle(s)
      const padX = parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight)
      const padY = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom)
      w = s.clientWidth - padX
      h = s.clientHeight - padY
    }
    const dpr = devicePixelRatio || 1
    this.setAvailSize(Math.max(0, Math.round(w * dpr)), Math.max(0, Math.round(h * dpr)))
  }

  get renderKind() { return this.kind }
  get countersRef() { return this.counters }
  /** 绑定的画布：调用方据此判断 DOM 里的画布是否已被替换 */
  get canvasElement() { return this.canvas }

  // ---------- WebGL 初始化 ----------

  private initGL() {
    const gl = this.gl!
    gl.disable(gl.DEPTH_TEST)
    gl.disable(gl.BLEND)
    gl.pixelStorei(gl.UNPACK_ALIGNMENT, 1)
    gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, false)

    this.blitProg = program(gl, VERT, FRAG_RGBA)
    this.yuvProg = program(gl, VERT, FRAG_YUV)

    this.vbo = gl.createBuffer()!
    gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo)
    gl.bufferData(gl.ARRAY_BUFFER, VERTICES, gl.STATIC_DRAW)

    this.blitLoc = {
      source: gl.getUniformLocation(this.blitProg, 'source'),
      texelSize: gl.getUniformLocation(this.blitProg, 'texelSize'),
      zoom: gl.getUniformLocation(this.blitProg, 'zoom'),
      uM: gl.getUniformLocation(this.blitProg, 'uM'),
      uT: gl.getUniformLocation(this.blitProg, 'uT'),
    }
    this.yuvLoc = {
      yPlane: gl.getUniformLocation(this.yuvProg, 'yPlane'),
      uPlane: gl.getUniformLocation(this.yuvProg, 'uPlane'),
      vPlane: gl.getUniformLocation(this.yuvProg, 'vPlane'),
      lumaSize: gl.getUniformLocation(this.yuvProg, 'lumaSize'),
      chromaSize: gl.getUniformLocation(this.yuvProg, 'chromaSize'),
      kY: gl.getUniformLocation(this.yuvProg, 'kY'),
      kc: gl.getUniformLocation(this.yuvProg, 'kc'),
      ccm: gl.getUniformLocation(this.yuvProg, 'ccm'),
      uM: gl.getUniformLocation(this.yuvProg, 'uM'),
      uT: gl.getUniformLocation(this.yuvProg, 'uT'),
    }

    this.texRGBA = this.makeTexture()
    this.texY = this.makeTexture()
    this.texU = this.makeTexture()
    this.texV = this.makeTexture()
    // 采样器 uniform 不在这里设：此时没有 program 在用，uniform1i 会被 WebGL
    // 静默忽略，导致采样器停在默认值 0（三个平面都读亮度纹理 → 画面泛绿）。
    // 必须在 drawYUV 里 useProgram 之后再设（见 drawYUV）。
  }

  private makeTexture() {
    const gl = this.gl!
    const t = gl.createTexture()!
    gl.bindTexture(gl.TEXTURE_2D, t)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    return t
  }

  private onLost = (e: Event) => {
    // preventDefault 是「由我来处理恢复」的声明：不 preventDefault 的话
    // 浏览器不会尝试重建上下文。同一画布的上下文类型永久锁定（WebGL 丢失后
    // 拿不到 2d），所以只能等恢复，不能就地换渲染后端。
    e.preventDefault()
    this.clearPending()
    window.setTimeout(() => {
      if (this.closed || this.kind !== 'webgl') return
      if (this.gl && !this.gl.isContextLost()) return // 已恢复（onRestored 会重建资源）
      this.onUnrecoverable?.()
    }, 3000)
  }

  private onRestored = () => {
    if (this.closed || this.kind !== 'webgl') return
    // 恢复后是同一个 WebGL2 上下文对象，但所有 GL 资源已失效，需全部重建
    this.initGL()
    this.codedW = 0
    this.codedH = 0
    // YUV 纹理是新建的空纹理，lastYUV 仅剩尺寸/色彩信息（像素不在里面），
    // 重绘会从空纹理取到黑帧——清掉，等下一帧真实数据。
    // lastRGBA 是 VideoFrame（JS 侧句柄，与 GL 上下文无关），保留可靠重绘。
    this.lastYUV = null
  }

  private teardownGL() {
    const gl = this.gl
    if (!gl) return
    this.canvas.removeEventListener('webglcontextlost', this.onLost)
    this.canvas.removeEventListener('webglcontextrestored', this.onRestored)
    if (!gl.isContextLost()) {
      // 主动释放上下文，避免泄漏 GPU 资源
      try { gl.getExtension('WEBGL_lose_context')?.loseContext() } catch { /* 忽略 */ }
    }
    this.gl = null
  }

  // ---------- 画布尺寸 ----------

  private setAvailSize(w: number, h: number) {
    if (w === this.availW && h === this.availH) return
    this.availW = w
    this.availH = h
    if (this.codedW > 0) {
      this.relayout()
      this.repaint() // 尺寸变化：用最后一帧立即重采样
    }
  }

  /** 视图旋转：0/90/180/270（顺时针）。只旋转网页显示，不旋转设备。 */
  setViewRotation(deg: number) {
    const r = ((deg % 360) + 360) % 360
    if (r === this.rotation) return
    this.rotation = r
    if (this.codedW > 0) {
      this.relayout()
      this.repaint()
    }
  }

  /**
   * 客户端坐标 → 视频（编码）坐标，考虑视图旋转。
   * 触控/滚轮注入必须走这里，否则旋转后点哪儿打哪儿就错位了。
   */
  clientToVideo(clientX: number, clientY: number): { x: number; y: number } | null {
    if (!this.codedW || !this.codedH) return null
    const rect = this.canvas.getBoundingClientRect()
    if (rect.width < 1 || rect.height < 1) return null
    let u = (clientX - rect.left) / rect.width
    let v = (clientY - rect.top) / rect.height
    u = Math.min(1, Math.max(0, u))
    v = Math.min(1, Math.max(0, v))
    let sx = u, sy = v
    switch (this.rotation) {
      case 90: sx = v; sy = 1 - u; break
      case 180: sx = 1 - u; sy = 1 - v; break
      case 270: sx = 1 - v; sy = u; break
    }
    return {
      x: Math.max(0, Math.min(this.codedW - 1, Math.round(sx * (this.codedW - 1)))),
      y: Math.max(0, Math.min(this.codedH - 1, Math.round(sy * (this.codedH - 1)))),
    }
  }

  /**
   * 依据编码尺寸、视图旋转与舞台可用空间，设定画布的 CSS 尺寸与内部分辨率。
   * 等比 fit：不变形的前提下撑满可用区域（允许放大显示）。
   */
  private relayout() {
    const srcW = this.codedW, srcH = this.codedH
    if (!srcW || !srcH) return
    const swap = this.rotation % 180 !== 0
    const dispW = swap ? srcH : srcW
    const dispH = swap ? srcW : srcH
    const dpr = devicePixelRatio || 1
    const availW = this.availW || dispW * dpr
    const availH = this.availH || dispH * dpr
    const k = Math.min(availW / dispW, availH / dispH)
    const boxW = Math.max(1, Math.floor(dispW * k))
    const boxH = Math.max(1, Math.floor(dispH * k))
    // 内部分辨率：等于显示物理像素，但不超过源尺寸 2 倍（再高无细节增益）、
    // 单边不超过 4096（超大屏/高 DPR 时控制 GPU 开销）
    const bk = Math.min(2, 4096 / Math.max(dispW, dispH))
    const cw = Math.max(2, Math.min(boxW, Math.round(dispW * bk)))
    const ch = Math.max(2, Math.min(boxH, Math.round(dispH * bk)))
    const cssW = (boxW / dpr).toFixed(2) + 'px'
    const cssH = (boxH / dpr).toFixed(2) + 'px'
    if (this.canvas.style.width !== cssW) this.canvas.style.width = cssW
    if (this.canvas.style.height !== cssH) this.canvas.style.height = cssH
    if (this.canvas.width !== cw || this.canvas.height !== ch) {
      this.canvas.width = cw
      this.canvas.height = ch
    }
  }

  /**
   * 会话开始/结束：清空当前画面并复位内部尺寸，确保不残留上一会话画面。
   * 旧帧错觉由上层 loading 遮罩兜底。
   */
  newSession() {
    this.codedW = 0
    this.codedH = 0
    this.clearPending()
    this.lastRGBA?.close()
    this.lastRGBA = null
    this.lastYUV = null
    const w = this.canvas.width
    const h = this.canvas.height
    if (this.kind === 'webgl' && this.gl && !this.gl.isContextLost()) {
      const gl = this.gl
      gl.viewport(0, 0, w, h)
      gl.clearColor(0, 0, 0, 0)
      gl.clear(gl.COLOR_BUFFER_BIT)
    } else {
      this.ctx2d?.clearRect(0, 0, w, h)
    }
  }

  /**
   * 后台标签页节流：暂停期间不上屏（解码统计仍由上层负责）。
   * 恢复（p=false）时立即用保留的最新帧重绘：不重绘的话，画面要等
   * 下一个解码帧才刷新（静止画面下设备端可能长时间不出帧，表现为
   * 「切回来画面卡死」）。
   */
  setPaused(p: boolean) {
    if (this.paused === p) return
    this.paused = p
    if (p) this.clearPending()
    else this.repaint()
  }

  private clearPending() {
    if (this.pending?.kind === 'rgba') this.pending.frame.close()
    this.pending = null
  }

  // ---------- 输入 ----------

  /** WebCodecs 解码输出：VideoFrame 所有权移交渲染器（内部负责 close） */
  render(frame: VideoFrame) {
    if (this.closed) { frame.close(); return }
    this.setCodedSize(frame.displayWidth, frame.displayHeight)
    if (this.paused) {
      // 后台暂停期间不上屏，但保留最新帧句柄：返回前台时 repaint 立即
      // 恢复到当前画面（而不是切走前的旧帧）
      this.lastRGBA?.close()
      this.lastRGBA = frame
      return
    }

    this.lastRGBA?.close()
    this.lastRGBA = frame.clone()

    // 解码快于显示时只保留最新一帧（丢中间帧，减少 1 帧延迟）
    if (this.pending?.kind === 'rgba') {
      this.pending.frame.close()
      this.counters.skipped++
    }
    this.pending = { kind: 'rgba', frame }
    this.kick()
  }

  /** 源（编码）尺寸变化：记录并重排（尺寸不变时零开销，逐帧调用安全） */
  private setCodedSize(w: number, h: number) {
    if (!w || !h || (w === this.codedW && h === this.codedH)) return
    this.codedW = w
    this.codedH = h
    this.relayout()
  }

  /**
   * WebRTC 路径：<video> 元素直接作为纹理源，省掉一次 canvas 中转。
   * 同步绘制（调用方已在 rAF 循环里，再排一次 rAF 会平白多一帧延迟）。
   */
  renderVideo(video: HTMLVideoElement) {
    if (this.closed || this.paused) return
    const w = video.videoWidth
    const h = video.videoHeight
    if (!w || !h) return
    this.setCodedSize(w, h)
    this.drawVideo(video, w, h)
  }

  /**
   * WASM 软解输出：YUV 平面同步上传为纹理（texImage2D 会立即复制数据，
   * 返回后可安全丢弃 WASM 堆视图），再按 rAF 节流上屏。
   */
  renderYUV(f: YUVFrame) {
    if (this.closed) return
    const x = Math.max(0, Math.min(f.lumaWidth - 1, f.cropLeft | 0))
    const y = Math.max(0, Math.min(f.lumaHeight - 1, f.cropTop | 0))
    const cw = Math.max(2, Math.min(f.lumaWidth - x, f.cropWidth | 0))
    const ch = Math.max(2, Math.min(f.lumaHeight - y, f.cropHeight | 0))
    this.setCodedSize(cw, ch)

    if (this.kind !== 'webgl') {
      if (this.paused) return // 2D 转换吃 CPU，后台不做：恢复后由下一帧接上
      this.yuvToRGBA2D(f, x, y, cw, ch)
      return
    }
    // 上下文丢失时直接丢帧（onLost 已负责上报不可恢复），
    // 此处不能退回 2D：同一画布拿不到第二种上下文
    if (!this.gl || this.gl.isContextLost()) return

    const gl = this.gl
    const rowLength = gl.UNPACK_ROW_LENGTH
    const chw = Math.max(1, cw >> 1)
    const chh = Math.max(1, ch >> 1)

    gl.pixelStorei(rowLength, f.stride)
    gl.bindTexture(gl.TEXTURE_2D, this.texY)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.LUMINANCE, cw, ch, 0, gl.LUMINANCE, gl.UNSIGNED_BYTE,
      f.y.subarray(y * f.stride + x))
    gl.pixelStorei(rowLength, f.chromaStride)
    gl.bindTexture(gl.TEXTURE_2D, this.texU)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.LUMINANCE, chw, chh, 0, gl.LUMINANCE, gl.UNSIGNED_BYTE,
      f.u.subarray((y >> 1) * f.chromaStride + (x >> 1)))
    gl.bindTexture(gl.TEXTURE_2D, this.texV)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.LUMINANCE, chw, chh, 0, gl.LUMINANCE, gl.UNSIGNED_BYTE,
      f.v.subarray((y >> 1) * f.chromaStride + (x >> 1)))
    gl.pixelStorei(rowLength, 0)

    this.lastYUV = { width: cw, height: ch, limitedRange: f.limitedRange, bt709: f.bt709 }
    // 后台暂停：纹理已更新即可，返回前台由 repaint 上屏（不排 rAF，rAF 也不会跑）
    if (this.paused) return
    this.pending = { kind: 'yuv', width: cw, height: ch, limitedRange: f.limitedRange, bt709: f.bt709 }
    this.kick()
  }

  private kick() {
    if (!this.raf) this.raf = requestAnimationFrame(this.flush)
  }

  private repaint() {
    if (this.paused || this.closed || this.pending) return // 已有待绘帧
    if (this.lastRGBA) this.pending = { kind: 'rgba', frame: this.lastRGBA.clone() }
    else if (this.lastYUV) this.pending = { kind: 'yuv', ...this.lastYUV }
    else return
    this.kick()
  }

  private flush = () => {
    this.raf = 0
    if (this.closed || this.paused) return
    const p = this.pending
    this.pending = null
    if (!p) return
    if (p.kind === 'rgba') this.drawRGBA(p.frame)
    else this.drawYUV(p.width, p.height, p.limitedRange, p.bt709)
    this.counters.displayed++
  }

  private drawRGBA(frame: VideoFrame) {
    if (this.kind === 'webgl' && this.gl && !this.gl.isContextLost()) {
      const gl = this.gl
      gl.bindTexture(gl.TEXTURE_2D, this.texRGBA)
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, frame)
      this.drawBlit(frame.displayWidth, frame.displayHeight)
      this.counters.rendered++
      frame.close()
      return
    }
    if (this.ctx2d) {
      this.blit2D(frame)
      this.counters.rendered++
    }
    frame.close()
  }

  private drawVideo(video: HTMLVideoElement, w: number, h: number) {
    if (this.kind === 'webgl' && this.gl && !this.gl.isContextLost()) {
      const gl = this.gl
      gl.bindTexture(gl.TEXTURE_2D, this.texRGBA)
      gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, video)
      this.drawBlit(w, h)
      this.counters.rendered++
      return
    }
    if (this.ctx2d) {
      this.blit2D(video)
      this.counters.rendered++
    }
  }

  /**
   * canvas2d 后备路径：源图绕画布中心顺时针旋转 rotation 角后铺满画布盒。
   * 画布盒已按旋转后的比例分配（relayout），故铺满即等比、不变形。
   */
  private blit2D(src: CanvasImageSource) {
    const ctx = this.ctx2d!
    const cw = this.canvas.width, ch = this.canvas.height
    ctx.clearRect(0, 0, cw, ch)
    ctx.save()
    ctx.translate(cw / 2, ch / 2)
    if (this.rotation) ctx.rotate(this.rotation * Math.PI / 180)
    // 90/270：源图的宽高对应画布盒的高宽
    const swap = this.rotation % 180 !== 0
    const w = swap ? ch : cw
    const h = swap ? cw : ch
    ctx.drawImage(src, -w / 2, -h / 2, w, h)
    ctx.restore()
  }

  /**
   * 视图旋转的「显示 uv → 源 uv」变换（着色器里 p = uM*uv + uT）。
   * 源纹理始终是未旋转的画面，画布盒已按旋转后比例分配，
   * 采样时把显示坐标转回源坐标即可实现纯显示的旋转。
   * 返回 [m00,m10,m01,m11]（列主序）与 [t0,t1]。逐帧调用，故按角度缓存。
   */
  private rotCache: { deg: number; m: Float32Array; t: Float32Array } | null = null
  private rotationUV(): { m: Float32Array; t: Float32Array } {
    if (this.rotCache && this.rotCache.deg === this.rotation) {
      return { m: this.rotCache.m, t: this.rotCache.t }
    }
    let m: number[], t: number[]
    switch (this.rotation) {
      // 顺时针 90°：源 uv = (v, 1-u)
      case 90: m = [0, -1, 1, 0]; t = [0, 1]; break
      case 180: m = [-1, 0, 0, -1]; t = [1, 1]; break
      // 顺时针 270°：源 uv = (1-v, u)
      case 270: m = [0, 1, -1, 0]; t = [1, 0]; break
      default: m = [1, 0, 0, 1]; t = [0, 0]
    }
    const fm = new Float32Array(m), ft = new Float32Array(t)
    this.rotCache = { deg: this.rotation, m: fm, t: ft }
    return { m: fm, t: ft }
  }

  private drawBlit(srcW: number, srcH: number) {
    const gl = this.gl
    if (!gl) return
    gl.viewport(0, 0, gl.drawingBufferWidth, gl.drawingBufferHeight)
    gl.useProgram(this.blitProg)
    const xy = gl.getAttribLocation(this.blitProg, 'xy')
    gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo)
    gl.vertexAttribPointer(xy, 2, gl.FLOAT, false, 0, 0)
    gl.enableVertexAttribArray(xy)
    gl.activeTexture(gl.TEXTURE0)
    gl.bindTexture(gl.TEXTURE_2D, this.texRGBA)
    gl.uniform1i(this.blitLoc.source, 0)
    gl.uniform2f(this.blitLoc.texelSize, 1 / Math.max(1, srcW), 1 / Math.max(1, srcH))
    // 90/270 时源宽对应画布高，缩放比按对应轴取
    const swap = this.rotation % 180 !== 0
    const along = swap ? this.canvas.height : this.canvas.width
    gl.uniform1f(this.blitLoc.zoom, along / Math.max(1, srcW))
    const rt = this.rotationUV()
    gl.uniformMatrix2fv(this.blitLoc.uM, false, rt.m)
    gl.uniform2f(this.blitLoc.uT, rt.t[0], rt.t[1])
    gl.drawArrays(gl.TRIANGLES, 0, 3)
    gl.flush()
  }

  private drawYUV(w: number, h: number, limitedRange: boolean, bt709: boolean) {
    const gl = this.gl
    if (!gl || gl.isContextLost()) return
    gl.viewport(0, 0, gl.drawingBufferWidth, gl.drawingBufferHeight)
    gl.useProgram(this.yuvProg)
    const xy = gl.getAttribLocation(this.yuvProg, 'xy')
    gl.bindBuffer(gl.ARRAY_BUFFER, this.vbo)
    gl.vertexAttribPointer(xy, 2, gl.FLOAT, false, 0, 0)
    gl.enableVertexAttribArray(xy)

    gl.activeTexture(gl.TEXTURE0); gl.bindTexture(gl.TEXTURE_2D, this.texY)
    gl.activeTexture(gl.TEXTURE1); gl.bindTexture(gl.TEXTURE_2D, this.texU)
    gl.activeTexture(gl.TEXTURE2); gl.bindTexture(gl.TEXTURE_2D, this.texV)
    // 采样器 uniform 必须在 useProgram 之后设：无绑定程序时 uniform1i 被静默忽略，
    // 采样器会停在默认值 0，三个平面全读纹理单元 0（亮度）→ 画面整体泛绿
    gl.uniform1i(this.yuvLoc.yPlane, 0)
    gl.uniform1i(this.yuvLoc.uPlane, 1)
    gl.uniform1i(this.yuvLoc.vPlane, 2)

    gl.uniform2f(this.yuvLoc.lumaSize, w, h)
    gl.uniform2f(this.yuvLoc.chromaSize, Math.max(1, w / 2), Math.max(1, h / 2))

    // 只做「减偏移」，增益交给矩阵：y = Y/255 − 黑电平，u/v = C/255 − 128/255（色度中心）
    const off = yuvOffsets(limitedRange)
    gl.uniform3f(this.yuvLoc.kY, 1, off.yOff, 0)
    gl.uniform3f(this.yuvLoc.kc, 1, off.cOff, 0)
    const m = limitedRange ? (bt709 ? CCM_709_LIMITED : CCM_601_LIMITED) : (bt709 ? CCM_709_FULL : CCM_601_FULL)
    // uniformMatrix3fv 要列主序，上表按行主序书写，故转置
    gl.uniformMatrix3fv(this.yuvLoc.ccm, false, new Float32Array([m[0], m[3], m[6], m[1], m[4], m[7], m[2], m[5], m[8]]))
    const rt = this.rotationUV()
    gl.uniformMatrix2fv(this.yuvLoc.uM, false, rt.m)
    gl.uniform2f(this.yuvLoc.uT, rt.t[0], rt.t[1])
    gl.drawArrays(gl.TRIANGLES, 0, 3)
    gl.flush()
    this.counters.rendered++
  }

  /**
   * 无 WebGL2 时的 CPU 转换兜底：帧率有限，仅保证可用。
   * 先转成源尺寸的 RGBA 放进离屏画布，再交给 blit2D —— 这样旋转与
   * 「按画布盒铺满」都由同一条路径负责，不会覆盖 relayout 算好的尺寸
   * （直接 putImageData 到主画布会忽略 transform 且改坏画布尺寸）。
   */
  private scratch: HTMLCanvasElement | null = null
  private scratchCtx: CanvasRenderingContext2D | null = null
  private yuvToRGBA2D(f: YUVFrame, x: number, y: number, w: number, h: number) {
    if (!this.ctx2d) return
    if (!this.scratch) {
      this.scratch = document.createElement('canvas')
      this.scratchCtx = this.scratch.getContext('2d')
    }
    const sc = this.scratch, sctx = this.scratchCtx
    if (!sc || !sctx) return
    if (sc.width !== w || sc.height !== h) { sc.width = w; sc.height = h }

    const out = new Uint8ClampedArray(w * h * 4)
    // 与着色器同约定（0-255 空间）：亮度减黑电平、色度减 128 中心，增益全在矩阵。
    // R = m0*(Y-y0) + m2*(Cr-128) —— 标准 BT.601/701 系数，无需额外缩放。
    const y0 = f.limitedRange ? 16 : 0
    const m = f.limitedRange ? (f.bt709 ? CCM_709_LIMITED : CCM_601_LIMITED) : (f.bt709 ? CCM_709_FULL : CCM_601_FULL)
    for (let j = 0; j < h; j++) {
      const row = (j + y) * f.stride + x
      const crow = ((j >> 1) + (y >> 1)) * f.chromaStride + (x >> 1)
      for (let i = 0; i < w; i++) {
        const Y = f.y[row + i] - y0
        const Cb = f.u[crow + (i >> 1)] - 128
        const Cr = f.v[crow + (i >> 1)] - 128
        const o = (j * w + i) * 4
        out[o] = Math.max(0, Math.min(255, m[0] * Y + m[2] * Cr))
        out[o + 1] = Math.max(0, Math.min(255, m[3] * Y + m[4] * Cb + m[5] * Cr))
        out[o + 2] = Math.max(0, Math.min(255, m[6] * Y + m[7] * Cb))
        out[o + 3] = 255
      }
    }
    sctx.putImageData(new ImageData(out, w, h), 0, 0)
    this.blit2D(sc)
    this.counters.rendered++
    this.counters.displayed++
  }

  dispose() {
    this.closed = true
    if (this.raf) cancelAnimationFrame(this.raf)
    this.raf = 0
    this.ro?.disconnect()
    this.clearPending()
    this.lastRGBA?.close()
    this.lastRGBA = null
    if (this.kind === 'webgl') {
      const gl = this.gl
      if (gl) {
        gl.deleteBuffer(this.vbo)
        gl.deleteTexture(this.texRGBA)
        gl.deleteTexture(this.texY)
        gl.deleteTexture(this.texU)
        gl.deleteTexture(this.texV)
        gl.deleteProgram(this.blitProg)
        gl.deleteProgram(this.yuvProg)
      }
      this.teardownGL()
    }
  }
}
