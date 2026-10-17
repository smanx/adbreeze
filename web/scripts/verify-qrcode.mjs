#!/usr/bin/env node
// QR 编码器交叉验证（Node 侧）：
//   对每组测试文本，用 Python qrcode 库生成参考矩阵，再检查本实现
//   （web/src/qrcode.ts）8 个掩码候选中是否有一个与参考矩阵完全一致。
//   一致 = 数据放置 / 功能图案 / 格式信息 / 版本信息 / Reed-Solomon 全部正确；
//   掩码评分若与参考实现不同，只会落在另一个有效掩码上（不影响可扫）。
//
// 用法：
//   node --experimental-strip-types web/scripts/verify-qrcode.mjs [pythonPath]
// 依赖：pip install qrcode（验证脚本专用，不进前端依赖）

import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { qrEncode, qrVersionOf } from '../src/qrcode.ts'

const python = process.argv[2] || 'python'
const pyScript = fileURLToPath(new URL('./verify-qrcode.py', import.meta.url))

const cases = [
  'https://carpet-services-etc-lines.trycloudflare.com', // 49B → v4
  'https://a.trycloudflare.com', // 27B → v2
  'https://some-really-long-random-subdomain-name-here.trycloudflare.com', // 66B → v5
  'https://x.trycloudflare.com/path?query=1&other=2', // 48B → v4
  '中文内容测试-投屏地址', // UTF-8 多字节
  'https://ui-test-9876.trycloudflare.com', // 39B → v3
]

let failed = 0
for (const text of cases) {
  const ref = JSON.parse(execFileSync(python, [pyScript, text], { encoding: 'utf8' }))
  const mine = qrEncode(text)
  const myVersion = qrVersionOf(text)
  const sizeOk = mine.length === ref.matrix.length
  // 逐字节比较
  let exact = sizeOk
  if (sizeOk) {
    for (let r = 0; r < mine.length && exact; r++) {
      for (let c = 0; c < mine.length; c++) {
        if ((mine[r][c] ? 1 : 0) !== ref.matrix[r][c]) { exact = false; break }
      }
    }
  }
  // 若最优掩码不同，尝试全部 8 个掩码做候选对照
  let anyMask = exact
  if (!exact) {
    for (let mask = 0; mask < 8; mask++) {
      const m = qrEncode(text, mask)
      if (m.length !== ref.matrix.length) continue
      let same = true
      for (let r = 0; r < m.length && same; r++) {
        for (let c = 0; c < m.length; c++) {
          if ((m[r][c] ? 1 : 0) !== ref.matrix[r][c]) { same = false; break }
        }
      }
      if (same) { anyMask = true; break }
    }
  }
  const ok = sizeOk && (exact || anyMask) && myVersion === ref.version
  if (!ok) failed++
  console.log(
    `${ok ? 'PASS' : 'FAIL'} v${myVersion}(ref v${ref.version}) ${mine.length}x${mine.length}(ref ${ref.matrix.length}x${ref.matrix.length[0] ?? 0}) ${exact ? '掩码一致' : anyMask ? '仅掩码不同（均为有效码）' : '矩阵不一致'} :: ${text.slice(0, 40)}`,
  )
}
console.log(failed === 0 ? 'ALL PASS' : `${failed} FAILED`)
process.exit(failed === 0 ? 0 : 1)
