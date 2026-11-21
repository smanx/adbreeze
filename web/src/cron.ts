// 前端 cron 解析：与后端 internal/scheduler/cron.go 的规则保持一致，
// 用于在任务编辑弹框里实时显示「这条规则是什么意思」或具体错误信息。
// 这样用户不用保存后去列表里找错误提示。
import { getLang, t } from './i18n'

export interface CronInfo {
  valid: boolean
  error: string
  description: string
}

type Field = { values: Set<number>; any: boolean }
type Parsed = { minute: Field; hour: Field; dom: Field; month: Field; dow: Field }

const LIMITS = [
  { min: 0, max: 59, key: 'tasks.cronMinute' as const },
  { min: 0, max: 23, key: 'tasks.cronHour' as const },
  { min: 1, max: 31, key: 'tasks.cronDay' as const },
  { min: 1, max: 12, key: 'tasks.cronMonth' as const },
  { min: 0, max: 7, key: 'tasks.cronWeek' as const },
]

function digits(s: string): boolean { return /^\d+$/.test(s) }

function parseField(raw: string, idx: number): Field | string {
  const { min, max, key } = LIMITS[idx]
  const fieldName = t(key)
  if (!raw) return t('tasks.cronEmptyField')
  const values = new Set<number>()
  for (const part of raw.split(',')) {
    if (!part) return t('tasks.cronEmptyPart')
    let step = 1
    let body = part
    const slash = part.indexOf('/')
    if (slash >= 0) {
      body = part.slice(0, slash)
      const stepRaw = part.slice(slash + 1)
      const s = Number(stepRaw)
      if (!digits(stepRaw) || s <= 0) return t('tasks.cronBadStep', { v: stepRaw })
      step = s
    }
    let lo: number
    let hi: number
    if (body === '*') {
      lo = min; hi = max
    } else if (body.includes('-')) {
      const seg = body.split('-')
      const a = Number(seg[0]); const b = Number(seg[1])
      if (seg.length !== 2 || !digits(seg[0]) || !digits(seg[1]) || a > b) {
        return t('tasks.cronBadRange', { v: body })
      }
      lo = a; hi = b
    } else {
      if (!digits(body)) return t('tasks.cronBadValue', { v: body })
      lo = hi = Number(body)
    }
    if (lo < min || hi > max) {
      return t('tasks.cronOutOfRange', { field: fieldName, min, max })
    }
    for (let v = lo; v <= hi; v += step) values.add(v)
  }
  if (!values.size) return t('tasks.cronNoValues', { field: fieldName })
  // 周字段里 7 与 0 都表示周日，后端会归一化，这里做同样处理。
  if (idx === 4 && values.has(7)) { values.add(0); values.delete(7) }
  return { values, any: raw === '*' }
}

function sorted(f: Field): number[] { return Array.from(f.values).sort((a, b) => a - b) }
function single(f: Field): number | null {
  const v = sorted(f)
  return v.length === 1 ? v[0] : null
}
function hhmm(minute: number, hour: number): string {
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
}
function isEveryN(f: Field, n: number, max: number): boolean {
  if (n <= 0 || f.values.size !== Math.floor(max / n) + 1) return false
  for (let v = 0; v <= max; v += n) if (!f.values.has(v)) return false
  return true
}

function describe(p: Parsed, expr: string): string {
  const minutes = sorted(p.minute)
  const days = sorted(p.dow)
  const doms = sorted(p.dom)

  // */n * * * *：每 N 分钟
  if (p.hour.any && p.dom.any && p.month.any && p.dow.any && minutes[0] === 0) {
    const n = minutes[1]
    if (n !== undefined && isEveryN(p.minute, n, 59) && minutes.length > 1) {
      return t('tasks.cronEveryMinutes', { n })
    }
    if (minutes.length === 60) return t('tasks.cronEveryMinute')
  }

  const m = single(p.minute)
  const h = single(p.hour)
  if (m !== null && h !== null && p.month.any) {
    const time = hhmm(m, h)
    if (p.dom.any && p.dow.any) return t('tasks.cronDailyAt', { time })
    if (p.dom.any && !p.dow.any) {
      if (days.length === 5 && days[0] === 1 && days[4] === 5) return t('tasks.cronWeekdaysAt', { time })
      if (days.length === 2 && days.includes(0) && days.includes(6)) return t('tasks.cronWeekendsAt', { time })
      const names = getLang() === 'zh'
        ? ['日', '一', '二', '三', '四', '五', '六']
        : ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
      const label = days.map(d => names[d]).join(getLang() === 'zh' ? '、' : ', ')
      return t('tasks.cronWeeklyAt', { days: label, time })
    }
    if (p.dow.any && !p.dom.any && doms.length === 1) {
      return t('tasks.cronMonthlyAt', { day: doms[0], time })
    }
  }

  return t('tasks.cronCustom', { expr })
}

/** 解析并生成自然语言描述；出错时返回与后端一致的错误文案。 */
export function analyzeCron(expr: string): CronInfo {
  const fields = expr.trim().split(/\s+/).filter(Boolean)
  if (fields.length !== 5) {
    return {
      valid: false,
      error: t('tasks.cronFieldCount', { n: fields.length }),
      description: '',
    }
  }
  const parsed: Partial<Parsed> = {}
  const names: Array<keyof Parsed> = ['minute', 'hour', 'dom', 'month', 'dow']
  for (let i = 0; i < 5; i++) {
    const r = parseField(fields[i], i)
    if (typeof r === 'string') return { valid: false, error: `${t(LIMITS[i].key)}: ${r}`, description: '' }
    parsed[names[i]] = r
  }
  const p = parsed as Parsed
  return { valid: true, error: '', description: describe(p, expr.trim()) }
}
