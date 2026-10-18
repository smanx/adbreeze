// site 构建（--mode site）专用：把 ../screenshots 拷进 dist-site/screenshots/。
// 主应用构建（默认模式）不执行本脚本，因此截图不会进入嵌入二进制 / Docker 镜像。
import { cpSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const src = join(root, 'screenshots')
const dest = join(root, 'dist-site', 'screenshots')

mkdirSync(dest, { recursive: true })
cpSync(src, dest, { recursive: true })

console.log(`copied screenshots -> ${dest}`)
