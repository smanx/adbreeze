// 零依赖 QR 编码器（ISO/IEC 18004 子集）：
//   仅 byte 模式 + ECC 等级 M + 版本 1-10，含格式/版本信息 BCH、8 种掩码惩罚评分。
//
// 为什么自己写而不引第三方库：项目前端保持零运行时依赖（现有依赖全是框架级），
// 而二维码逻辑固定、可完整测试——矩阵正确性用 Python qrcode 库交叉验证
// （见 web/scripts/verify-qrcode.mjs 的对照思路与本次实施记录）。
//
// 容量：byte 模式 M 级下 v4 可容 62 字节、v10 可容 213 字节，
// 覆盖 https://<随机词>.trycloudflare.com 这类短 URL（实测约 49 字符）。

// ---------- GF(256) 算术（本原多项式 0x11D） ----------

const GF_EXP = new Uint8Array(512)
const GF_LOG = new Uint8Array(256)
;(() => {
  let x = 1
  for (let i = 0; i < 255; i++) {
    GF_EXP[i] = x
    GF_LOG[x] = i
    x <<= 1
    if (x & 0x100) x ^= 0x11d
  }
  for (let i = 255; i < 512; i++) GF_EXP[i] = GF_EXP[i - 255]
})()

function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0
  return GF_EXP[GF_LOG[a] + GF_LOG[b]]
}

// ---------- 规范表格（版本 1-10，ECC M） ----------

// [每块 EC 码字, [ [块数, 数据码字], ... ]]
const ECC_M: Array<[number, Array<[number, number]>]> = [
  [0, []], // 占位：版本从 1 起
  [10, [[1, 16]]],
  [16, [[1, 28]]],
  [26, [[1, 44]]],
  [18, [[2, 32]]],
  [24, [[2, 43]]],
  [16, [[4, 27]]],
  [18, [[4, 31]]],
  [22, [[2, 38], [2, 39]]],
  [22, [[3, 36], [2, 37]]],
  [26, [[4, 43], [1, 44]]],
]

// 对齐图案中心坐标
const ALIGN: Record<number, number[]> = {
  1: [], 2: [6, 18], 3: [6, 22], 4: [6, 26], 5: [6, 30],
  6: [6, 34], 7: [6, 22, 38], 8: [6, 24, 42], 9: [6, 26, 46], 10: [6, 28, 50],
}

// 数据区后的余位 bit 数
const REMAINDER: Record<number, number> = { 1: 0, 2: 7, 3: 7, 4: 7, 5: 7, 6: 7, 7: 0, 8: 0, 9: 0, 10: 0 }

const MIN_VERSION = 1
const MAX_VERSION = 10

// ---------- 数据结构 ----------

// 功能图案掩膜图：true = 功能模块（数据放置时跳过）
type Matrix = {
  size: number
  modules: boolean[][] // true = 黑
  reserved: boolean[][] // true = 功能图案占位
}

function newMatrix(size: number): Matrix {
  const modules: boolean[][] = []
  const reserved: boolean[][] = []
  for (let r = 0; r < size; r++) {
    modules.push(new Array(size).fill(false))
    reserved.push(new Array(size).fill(false))
  }
  return { size, modules, reserved }
}

// ---------- 编码流程 ----------

/** byte 模式的容量（字节数）：数据码字 - 模式指示(4bit) - 计数位(8/16bit) */
function byteCapacity(version: number): number {
  const dataCodewords = ECC_M[version][1].reduce((sum, [n, d]) => sum + n * d, 0)
  const countBits = version >= 10 ? 16 : 8
  return dataCodewords - 1 - countBits / 8 // 4bit 模式指示 = 0.5 字节
}

function chooseVersion(byteLen: number): number {
  for (let v = MIN_VERSION; v <= MAX_VERSION; v++) {
    if (byteCapacity(v) >= byteLen) return v
  }
  throw new Error(`数据过长（${byteLen} 字节）：QR 子集最大支持 ${byteCapacity(MAX_VERSION)} 字节`)
}

/** UTF-8 编码 + 模式/计数/数据/终止/填充，返回数据码字序列 */
function encodeData(bytes: Uint8Array, version: number): number[] {
  const dataCodewords = ECC_M[version][1].reduce((sum, [n, d]) => sum + n * d, 0)
  const bits: number[] = []
  const push = (val: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1)
  }
  push(0b0100, 4) // byte 模式
  if (version >= 10) push(bytes.length, 16)
  else push(bytes.length, 8)
  for (const b of bytes) push(b, 8)

  // 终止符（最多 4 位）+ 字节对齐
  const capacityBits = dataCodewords * 8
  push(0, Math.min(4, capacityBits - bits.length))
  while (bits.length % 8 !== 0) bits.push(0)

  const codewords: number[] = []
  for (let i = 0; i < bits.length; i += 8) {
    let b = 0
    for (let j = 0; j < 8; j++) b = (b << 1) | bits[i + j]
    codewords.push(b)
  }
  // 填充码字 0xEC / 0x11 交替
  const pads = [0xec, 0x11]
  for (let i = 0; codewords.length < dataCodewords; i++) codewords.push(pads[i % 2])
  return codewords
}

/** Reed-Solomon 编码：返回 ecLen 个 EC 码字（生成多项式 ∏(x-α^i)，综合除法） */
function rsEncode(data: number[], ecLen: number): number[] {
  // 生成多项式系数（升序：g[0]=常数项 … g[ecLen]=首项 1）
  const g = [1]
  for (let i = 0; i < ecLen; i++) {
    const ng = new Array(g.length + 1).fill(0)
    for (let j = 0; j < g.length; j++) {
      ng[j] ^= gfMul(g[j], GF_EXP[i])
      ng[j + 1] ^= g[j]
    }
    g.splice(0, g.length, ...ng)
  }
  // 综合除法按降序用（gen[0]=首项 1，与 res[i] 对齐后逐位消去）
  const gen = [...g].reverse()
  const res = new Array(data.length + ecLen).fill(0)
  for (let i = 0; i < data.length; i++) res[i] = data[i]
  for (let i = 0; i < data.length; i++) {
    const coef = res[i]
    if (coef === 0) continue
    for (let j = 0; j <= ecLen; j++) res[i + j] ^= gfMul(gen[j], coef)
  }
  return res.slice(data.length)
}

/** 分块交织：数据码字按块分组，各自追加 EC，再按规范交错 */
function interleaveBlocks(dataCodewords: number[], version: number): number[] {
  const [ecLen, groups] = ECC_M[version]
  const dataBlocks: number[][] = []
  const ecBlocks: number[][] = []
  let offset = 0
  for (const [count, len] of groups) {
    for (let i = 0; i < count; i++) {
      const block = dataCodewords.slice(offset, offset + len)
      offset += len
      dataBlocks.push(block)
      ecBlocks.push(rsEncode(block, ecLen))
    }
  }
  const out: number[] = []
  const maxData = Math.max(...dataBlocks.map(b => b.length))
  for (let i = 0; i < maxData; i++) {
    for (const b of dataBlocks) if (i < b.length) out.push(b[i])
  }
  for (let i = 0; i < ecLen; i++) {
    for (const b of ecBlocks) out.push(b[i])
  }
  return out
}

// ---------- 矩阵绘制 ----------

function drawFunctionPatterns(m: Matrix, version: number) {
  const { size, modules, reserved } = m
  const set = (r: number, c: number, dark: boolean) => {
    modules[r][c] = dark
    reserved[r][c] = true
  }

  // 三个探测图案（含分隔符）
  for (const [or_, oc] of [[0, 0], [0, size - 7], [size - 7, 0]] as Array<[number, number]>) {
    for (let r = -1; r <= 7; r++) {
      for (let c = -1; c <= 7; c++) {
        const rr = or_ + r
        const cc = oc + c
        if (rr < 0 || rr >= size || cc < 0 || cc >= size) continue
        const inRing = r >= 0 && r <= 6 && c >= 0 && c <= 6
        const dark = inRing && (r === 0 || r === 6 || c === 0 || c === 6 || (r >= 2 && r <= 4 && c >= 2 && c <= 4))
        set(rr, cc, dark)
      }
    }
  }

  // 定时图案（第 6 行/列）
  for (let i = 8; i < size - 8; i++) {
    const dark = i % 2 === 0
    if (!reserved[6][i]) set(6, i, dark)
    if (!reserved[i][6]) set(i, 6, dark)
  }

  // 对齐图案（避开探测图案）
  const centers = ALIGN[version]
  for (const r of centers) {
    for (const c of centers) {
      if (reserved[r][c]) continue
      for (let dr = -2; dr <= 2; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          const dark = Math.max(Math.abs(dr), Math.abs(dc)) !== 1
          set(r + dr, c + dc, dark)
        }
      }
    }
  }

  // 保留格式信息的区域（先占位，最后填）
  // 第一份：row 8 cols 0-8 + col 8 rows 0-8（row/col 6 是定时图案，已占用会跳过）
  for (let i = 0; i < 9; i++) {
    if (!reserved[8][i]) set(8, i, false)
    if (!reserved[i][8]) set(i, 8, false)
  }
  // 第二份：竖 7（col 8, rows size-7..size-1）+ 横 8（row 8, cols size-8..size-1）。
  // 暗模块 (size-8, 8) 不属于格式条带——它在竖条终点的下一格，无冲突。
  for (let i = 0; i < 7; i++) {
    if (!reserved[size - 1 - i][8]) set(size - 1 - i, 8, false)
  }
  for (let i = 0; i < 8; i++) {
    if (!reserved[8][size - 1 - i]) set(8, size - 1 - i, false)
  }
  // 暗模块（格式信息的一部分）
  set(size - 8, 8, true)

  // 版本信息（v≥7，先占位）
  if (version >= 7) {
    for (let i = 0; i < 18; i++) {
      const r = Math.floor(i / 3)
      const c = i % 3
      set(size - 11 + c, r, false)
      set(r, size - 11 + c, false)
    }
  }
}

// drawFormatInfo 填两份格式信息。位序为 MSB 在前（bit14 在探测图案侧首格），
// 从探测图案向外排（ISO 18004 图 7；本布局经 Python qrcode 参考矩阵反解验证）。
function drawFormatInfo(m: Matrix, mask: number) {
	// ECC M = 00，掩码 3 位，BCH(15,5) + 异或 0x5412
	const data = mask
	let rem = data
	for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
	const bits = ((data << 10) | rem) ^ 0x5412

	const { size, modules } = m
	const bitAt = (i: number) => ((bits >>> (14 - i)) & 1) === 1

	// 第一份：从 (8,0) 沿 row 8 向右，再沿 col 8 向下
	const pos1: Array<[number, number]> = [
		[8, 0], [8, 1], [8, 2], [8, 3], [8, 4], [8, 5], [8, 7], [8, 8], [7, 8],
		[5, 8], [4, 8], [3, 8], [2, 8], [1, 8], [0, 8],
	]
	pos1.forEach(([r, c], i) => { modules[r][c] = bitAt(i) })
	// 第二份：竖 7 格从底部向上（col 8, rows size-1..size-7）持 b14..b8，
	// 接横 8 格从左向右（row 8, cols size-8..size-1）持 b7..b0
	// （布局经 Python qrcode 参考矩阵反解验证）
	const pos2: Array<[number, number]> = []
	for (let i = 0; i < 7; i++) pos2.push([size - 1 - i, 8])
	for (let i = 0; i < 8; i++) pos2.push([8, size - 8 + i])
	pos2.forEach(([r, c], i) => { modules[r][c] = bitAt(i) })
	modules[size - 8][8] = true // 暗模块恒为 1
}

function drawVersionInfo(m: Matrix, version: number) {
  if (version < 7) return
  // BCH(18,6) + 异或 0x1F25
  let rem = version
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
  const bits = (version << 12) | rem

  const { size, modules } = m
  for (let i = 0; i < 18; i++) {
    const bit = ((bits >>> i) & 1) === 1
    const r = Math.floor(i / 3)
    const c = i % 3
    modules[size - 11 + c][r] = bit
    modules[r][size - 11 + c] = bit
  }
}

// ---------- 数据放置 + 掩码 ----------

function placeData(m: Matrix, codewords: number[], version: number) {
  const { size, modules, reserved } = m
  const bits: number[] = []
  for (const cw of codewords) {
    for (let i = 7; i >= 0; i--) bits.push((cw >>> i) & 1)
  }
  const rem = REMAINDER[version] ?? 0
  for (let i = 0; i < rem; i++) bits.push(0)

  let idx = 0
  let upward = true
  for (let col = size - 1; col >= 1; col -= 2) {
    if (col === 6) col = 5 // 跳过定时图案列
    for (let step = 0; step < size; step++) {
      const row = upward ? size - 1 - step : step
      for (const c of [col, col - 1]) {
        if (reserved[row][c]) continue
        if (idx < bits.length) modules[row][c] = bits[idx] === 1
        else modules[row][c] = false
        idx++
      }
    }
    upward = !upward
  }
}

function maskBit(mask: number, r: number, c: number): boolean {
  switch (mask) {
    case 0: return (r + c) % 2 === 0
    case 1: return r % 2 === 0
    case 2: return c % 3 === 0
    case 3: return (r + c) % 3 === 0
    case 4: return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0
    case 5: return ((r * c) % 2) + ((r * c) % 3) === 0
    case 6: return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0
    case 7: return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0
    default: return false
  }
}

function applyMask(m: Matrix, mask: number) {
  const { size, modules, reserved } = m
  for (let r = 0; r < size; r++) {
    for (let c = 0; c < size; c++) {
      if (reserved[r][c]) continue
      if (maskBit(mask, r, c)) modules[r][c] = !modules[r][c]
    }
  }
}

function penalty(m: Matrix): number {
  const { size, modules } = m
  let score = 0

  // 规则 1：行/列连续同色 ≥5
  for (let r = 0; r < size; r++) {
    let run = 1
    for (let c = 1; c < size; c++) {
      if (modules[r][c] === modules[r][c - 1]) run++
      else { if (run >= 5) score += 3 + (run - 5); run = 1 }
    }
    if (run >= 5) score += 3 + (run - 5)
  }
  for (let c = 0; c < size; c++) {
    let run = 1
    for (let r = 1; r < size; r++) {
      if (modules[r][c] === modules[r - 1][c]) run++
      else { if (run >= 5) score += 3 + (run - 5); run = 1 }
    }
    if (run >= 5) score += 3 + (run - 5)
  }

  // 规则 2：2x2 同色块
  for (let r = 0; r < size - 1; r++) {
    for (let c = 0; c < size - 1; c++) {
      const v = modules[r][c]
      if (v === modules[r][c + 1] && v === modules[r + 1][c] && v === modules[r + 1][c + 1]) score += 3
    }
  }

  // 规则 3：类探测图案的 1:1:3:1:1 序列（简化：按规范序列匹配）
  const seq = [true, false, true, true, true, false, true, false, false, false, false]
  const seqR = [false, false, false, false, true, false, true, true, true, false, true]
  const matchAt = (r: number, c: number, dr: number, dc: number, s: boolean[]) =>
    s.every((v, i) => modules[r + dr * i]?.[c + dc * i] === v)
  for (let r = 0; r < size; r++) {
    for (let c = 0; c + 11 <= size; c++) {
      if (matchAt(r, c, 0, 1, seq) || matchAt(r, c, 0, 1, seqR)) score += 40
    }
  }
  for (let c = 0; c < size; c++) {
    for (let r = 0; r + 11 <= size; r++) {
      if (matchAt(r, c, 1, 0, seq) || matchAt(r, c, 1, 0, seqR)) score += 40
    }
  }

  // 规则 4：黑白比例
  let dark = 0
  for (let r = 0; r < size; r++) for (let c = 0; c < size; c++) if (modules[r][c]) dark++
  const pct = (dark * 100) / (size * size)
  score += Math.floor(Math.abs(pct - 50) / 5) * 10
  return score
}

// ---------- 对外接口 ----------

/**
 * 编码文本为 QR 模块矩阵（true = 黑）。
 * 支持 UTF-8（byte 模式按 UTF-8 字节计长）；超长或空文本抛错。
 * forceMask（0-7）可指定掩码——仅测试/对照用；默认按惩罚评分选最优。
 */
export function qrEncode(text: string, forceMask = -1): boolean[][] {
  const bytes = new TextEncoder().encode(text)
  if (bytes.length === 0) throw new Error('QR 编码：内容为空')
  const version = chooseVersion(bytes.length)
  const codewords = interleaveBlocks(encodeData(bytes, version), version)

  let best: Matrix | null = null
  let bestScore = Infinity
  for (let mask = 0; mask < 8; mask++) {
    if (forceMask >= 0 && mask !== forceMask) continue
    const m = newMatrix(version * 4 + 17)
    drawFunctionPatterns(m, version)
    placeData(m, codewords, version)
    applyMask(m, mask)
    drawFormatInfo(m, mask)
    drawVersionInfo(m, version)
    const s = penalty(m)
    if (s < bestScore) {
      bestScore = s
      best = m
    }
  }
  if (!best) throw new Error('QR 编码失败')
  return best.modules
}

/** 生成 SVG path（深色模块并集），供 <path d> 直接使用；scale 为每模块像素 */
export function qrToPath(modules: boolean[][], scale = 1): string {
  let d = ''
  for (let r = 0; r < modules.length; r++) {
    for (let c = 0; c < modules.length; c++) {
      if (modules[r][c]) d += `M${c * scale} ${r * scale}h${scale}v${scale}h-${scale}z`
    }
  }
  return d
}

/** QR 版本号（仅测试/调试用） */
export function qrVersionOf(text: string): number {
  return chooseVersion(new TextEncoder().encode(text).length)
}
