export type TrafficRangeError = 'rangeIncomplete' | 'rangeInvalid' | 'rangeReversed' | 'rangeTooLong'

function dateValue(value: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null
  const date = new Date(`${value}T00:00:00Z`)
  return Number.isFinite(date.valueOf()) && date.toISOString().slice(0, 10) === value ? date : null
}

// Dates are calendar dates in the chosen zone. The API resolves the actual
// instants, including DST; UTC here only compares the selected calendar days.
export function trafficRangeError(startDate?: string | null, endDate?: string | null, timezone = 'UTC'): TrafficRangeError | null {
  if (!startDate && !endDate) return null
  if (!startDate || !endDate) return 'rangeIncomplete'
  const start = dateValue(startDate)
  const end = dateValue(endDate)
  if (!start || !end) return 'rangeInvalid'
  // The backend excludes negative Unix times; Shanghai's first local day in
  // 1970 still starts before the Unix epoch.
  if (startDate < (timezone === 'Asia/Shanghai' ? '1970-01-02' : '1970-01-01')) return 'rangeInvalid'
  if (end < start) return 'rangeReversed'
  const limit = new Date(start)
  limit.setUTCFullYear(start.getUTCFullYear() + 3)
  const exclusiveEnd = new Date(end)
  exclusiveEnd.setUTCDate(end.getUTCDate() + 1)
  return exclusiveEnd > limit ? 'rangeTooLong' : null
}
