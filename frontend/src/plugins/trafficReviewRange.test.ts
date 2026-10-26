import { describe, expect, it } from 'vitest'
import { defaultTrafficDateRange, trafficRangeError } from './trafficReviewRange'

describe('default traffic date range', () => {
  it('uses the selected timezone calendar date across UTC month boundaries', () => {
    expect(defaultTrafficDateRange(new Date('2026-11-01T06:30:00Z'))).toEqual({ startDate: '2026-10-01', endDate: '2026-10-31' })
    expect(defaultTrafficDateRange(new Date('2026-11-01T06:30:00Z'), 'UTC')).toEqual({ startDate: '2026-11-01', endDate: '2026-11-01' })
  })

  it('handles month rollover, leap day and daylight-saving transitions', () => {
    expect(defaultTrafficDateRange(new Date('2026-03-01T07:30:00Z'))).toEqual({ startDate: '2026-02-01', endDate: '2026-02-28' })
    expect(defaultTrafficDateRange(new Date('2024-02-29T20:00:00Z'))).toEqual({ startDate: '2024-02-01', endDate: '2024-02-29' })
    expect(defaultTrafficDateRange(new Date('2026-03-08T10:30:00Z'))).toEqual({ startDate: '2026-03-01', endDate: '2026-03-08' })
  })
})

describe('traffic date range', () => {
  it('accepts rolling mode, an inclusive single day and exactly three calendar years', () => {
    expect(trafficRangeError('', '')).toBeNull()
    expect(trafficRangeError('2026-10-18', '2026-10-18')).toBeNull()
    expect(trafficRangeError('2023-01-01', '2025-12-31')).toBeNull()
    expect(trafficRangeError('2024-02-29', '2027-02-28')).toBeNull()
  })
  it('rejects missing, invalid, reversed and over-limit dates', () => {
    expect(trafficRangeError('2026-10-18', '')).toBe('rangeIncomplete')
    expect(trafficRangeError('', '2026-10-18')).toBe('rangeIncomplete')
    expect(trafficRangeError('2026-02-30', '2026-03-01')).toBe('rangeInvalid')
    expect(trafficRangeError('1969-12-31', '1970-01-01')).toBe('rangeInvalid')
    expect(trafficRangeError('1970-01-01', '1970-01-02', 'Asia/Shanghai')).toBe('rangeInvalid')
    expect(trafficRangeError('1970-01-01', '1970-01-02', 'America/Los_Angeles')).toBeNull()
    expect(trafficRangeError('2026-10-18', '2026-10-17')).toBe('rangeReversed')
    expect(trafficRangeError('2023-01-01', '2026-01-01')).toBe('rangeTooLong')
    expect(trafficRangeError('2024-02-29', '2027-03-01')).toBe('rangeTooLong')
  })
})
