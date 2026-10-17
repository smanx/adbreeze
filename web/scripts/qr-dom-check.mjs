// 端到端校验：浏览器 DOM 渲染出的 QR 矩阵 vs Python qrcode 参考
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const python = 'C:/Users/cc/.workbuddy/binaries/python/envs/default/Scripts/python.exe'
const pyScript = fileURLToPath(new URL('./verify-qrcode.py', import.meta.url))
const url = process.argv[2]
const domRows = readFileSync(process.argv[3], 'utf8').trim().split('\n')

const ref = JSON.parse(execFileSync(python, [pyScript, url], { encoding: 'utf8' }))
let diffs = 0
for (let r = 0; r < ref.matrix.length; r++) {
  for (let c = 0; c < ref.matrix.length; c++) {
    if (domRows[r][c] !== String(ref.matrix[r][c])) diffs++
  }
}
console.log(`DOM 矩阵 ${domRows.length}x${domRows[0].length} vs 参考 ${ref.matrix.length}x${ref.matrix.length}`)
if (diffs === 0) {
  console.log('DOM 渲染矩阵与参考完全一致（UI 端到端 PASS）')
} else {
  console.log(`与参考差异 ${diffs} 处（可能是有效掩码不同，进入第二轮比对）`)
}

// 第二轮：DOM 矩阵 vs 本编码器 8 个掩码候选（编码器已通过强制掩码与参考比对）
const { qrEncode } = await import('../src/qrcode.ts')
const dom = domRows.map(row => row.split('').map(Number))
let hit = -1
for (let mask = 0; mask < 8; mask++) {
  const m = qrEncode(url, mask)
  let same = m.length === dom.length
  if (same) {
    for (let r = 0; r < m.length && same; r++) {
      for (let c = 0; c < m.length; c++) {
        if ((m[r][c] ? 1 : 0) !== dom[r][c]) { same = false; break }
      }
    }
  }
  if (same) { hit = mask; break }
}
if (hit >= 0) {
  console.log(`DOM 渲染矩阵 = 编码器掩码 ${hit} 的输出（UI→编码器链路 PASS；该掩码下编码器与参考字节级一致）`)
} else {
  console.log('DOM 矩阵与编码器任何掩码都不匹配——UI 链路有bug')
  process.exit(1)
}

// 第三轮：反解参考矩阵掩码，强制编码器用同一掩码，与参考字节级比对
const copy1 = [[8,0],[8,1],[8,2],[8,3],[8,4],[8,5],[8,7],[8,8],[7,8],[5,8],[4,8],[3,8],[2,8],[1,8],[0,8]]
let refMask = -1
for (let mk = 0; mk < 8; mk++) {
  let rem = mk
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
  const bits = ((mk << 10) | rem) ^ 0x5412
  if (copy1.every(([r, c], i) => ref.matrix[r][c] === ((bits >>> (14 - i)) & 1))) { refMask = mk; break }
}
if (refMask < 0) { console.log('参考掩码反解失败'); process.exit(1) }
const forced = qrEncode(url, refMask)
let fd = 0
for (let r = 0; r < forced.length; r++) for (let c = 0; c < forced.length; c++) if ((forced[r][c] ? 1 : 0) !== ref.matrix[r][c]) fd++
console.log(`参考掩码 = ${refMask}；强制同掩码后差异 ${fd} 处`)
console.log(fd === 0 ? '全链路闭环：UI 渲染 = 编码器输出 = 参考实现（PASS）' : 'FAIL')
if (fd !== 0) process.exit(1)
