import { useMemo } from 'react'
import { qrEncode, qrToPath } from '../qrcode'

// 二维码渲染（零依赖）：编码器见 qrcode.ts，矩阵正确性用 Python qrcode 库
// 交叉验证（web/scripts/verify-qrcode.mjs）。深色模块固定 #000、白底 +
// 4 模块静默区——不跟随主题，保证任何背景下可扫。
export default function QrCode({ text, size = 132 }: { text: string; size?: number }) {
  const rendered = useMemo(() => {
    try {
      const modules = qrEncode(text)
      const quiet = 4
      const scale = size / (modules.length + quiet * 2)
      return { d: qrToPath(modules, scale), offset: quiet * scale }
    } catch {
      return null // 内容超长或为空：不渲染（调用方自行处理提示）
    }
  }, [text, size])

  if (!rendered) return null
  return (
    <svg
      className="qr-code"
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label={text}
    >
      <rect width={size} height={size} fill="#ffffff" />
      <path d={rendered.d} transform={`translate(${rendered.offset} ${rendered.offset})`} fill="#000000" />
    </svg>
  )
}
