import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import EgressChart from './EgressChart.vue'

vi.mock('vue-chartjs', () => ({ Line: { name: 'LineChart', props: ['data', 'options'], template: '<div />' } }))

describe('egress chart', () => {
  it('includes calendar dates across multiple days and preserves sample gaps', () => {
    const wrapper = mount(EgressChart, { props: { timezone: 'UTC', locale: 'zh-CN', label: '出站速率', points: [
      { time: Date.UTC(2023, 0, 1) / 1000, rxBytes: 0, txBytes: 0, rxBps: 0, txBps: 1024 ** 2, quality: 'ok' },
      { time: Date.UTC(2026, 0, 1) / 1000, rxBytes: 0, txBytes: 0, rxBps: 0, txBps: 0, quality: 'gap' },
    ] } })
    const chart = wrapper.findComponent({ name: 'LineChart' })
    const data = chart.props('data')
    expect(data.labels[0]).toContain('2023')
    expect(data.labels[1]).toContain('2026')
    expect(data.datasets[0].data).toEqual([1, null])
    expect(data.datasets[0].spanGaps).toBe(false)
    wrapper.unmount()
  })
})
