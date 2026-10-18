import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createVuetify } from 'vuetify'
import * as components from 'vuetify/components'
import { createI18n } from 'vue-i18n'
import { reactive } from 'vue'
import TrafficReview from './TrafficReview.vue'
import { createTrafficReviewPoller } from '@/plugins/trafficReview'
import { emptyTrafficReviewStates } from '@/types/trafficReview'
import { trafficReviewZh } from '@/locales/trafficReview'

vi.mock('@/plugins/trafficReview', () => ({ createTrafficReviewPoller: vi.fn() }))
vi.mock('@/components/trafficReview/EgressChart.vue', () => ({ default: { template: '<div />' } }))

afterEach(() => { document.body.innerHTML = '' })

describe('Chinese outbound page', () => {
  function page(summary: Record<string, unknown> = {}) {
    const states = reactive(emptyTrafficReviewStates())
    states.summary.data = summary
    const setQuery = vi.fn()
    vi.mocked(createTrafficReviewPoller).mockReturnValue({ states, start: vi.fn(), stop: vi.fn(), refresh: vi.fn(), setQuery, isLoading: () => false } as unknown as ReturnType<typeof createTrafficReviewPoller>)
    const wrapper = mount(TrafficReview, { attachTo: document.body, global: { plugins: [createVuetify({ components }), createI18n({ legacy: false, locale: 'zhHans', messages: { zhHans: { pages: { trafficReview: '出站流量' }, trafficReview: trafficReviewZh } } })] } })
    return { states, wrapper, setQuery }
  }

  it('sends complete ranges, explains invalid input, and clears to the last 24 hours', async () => {
    const { wrapper, setQuery } = page()
    const dates = wrapper.findAll('input[type="date"]')
    await dates[0]!.setValue('2026-10-01')
    expect(wrapper.get('[data-testid="range-error"]').text()).toBe('请同时填写开始和结束日期。')
    expect(setQuery).toHaveBeenLastCalledWith({ startDate: '2026-10-01', endDate: '', timezone: 'America/Los_Angeles', leg: '' })
    await dates[1]!.setValue('2026-10-18')
    expect(wrapper.find('[data-testid="range-error"]').exists()).toBe(false)
    expect(setQuery).toHaveBeenLastCalledWith({ startDate: '2026-10-01', endDate: '2026-10-18', timezone: 'America/Los_Angeles', leg: '' })
    await dates[1]!.setValue('2026-09-30')
    expect(wrapper.get('[data-testid="range-error"]').text()).toBe('结束日期不能早于开始日期。')
    const clear = wrapper.findAll('button').find(button => button.text() === '最近 24 小时')!
    await clear.trigger('click')
    expect(setQuery).toHaveBeenLastCalledWith({ timezone: 'America/Los_Angeles', leg: '' })
    wrapper.unmount()
  })

  it('distinguishes unobserved payloads from observed zero and retains negative differences', async () => {
    const { states, wrapper } = page({ lastSampleAt: 1000, txBytes: 1024, networkSampleCount: 1, networkEgressBytes: 3072, egressDifferenceBytes: -2048, proxySampleCount: 0, proxyUploadBytes: 0, proxyDownloadBytes: 0 })
    expect(wrapper.get('[data-testid="egress-difference"]').text()).toContain('-2.00 KiB')
    const advanced = wrapper.findAll('button').find(button => button.text().includes('其他测量值'))!
    await advanced.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="proxy-upload"]').text()).toContain('—')
    expect(wrapper.text()).toContain('暂无代理载荷记录')
    states.summary.data = { ...(states.summary.data as object), proxySampleCount: 1 }
    await flushPromises()
    expect(wrapper.get('[data-testid="proxy-upload"]').text()).toContain('0.00 KiB')
    expect(wrapper.get('[data-testid="proxy-download"]').text()).toContain('0.00 KiB')
    wrapper.unmount()
  })

  it('warns that other imported billing items are outside the five-item total', async () => {
    const { wrapper } = page({ billingOverview: { targetSKUs: [], totals: {}, standardTierReference: {}, syncStatus: {}, otherSKUsCount: 1 } })
    await wrapper.findAll('[role="tab"]')[1]!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="other-bill-items"]').text()).toContain('其他已导入计费项')
    expect(wrapper.text()).toContain('并非完整 Google 账单')
    wrapper.unmount()
  })

  it('separates expired destination details from retained summary totals', () => {
    const { wrapper } = page({ txBytes: 2048, lastSampleAt: 1000, destinationDetailsPartial: true })
    expect(wrapper.get('[data-testid="nic-egress"]').text()).toBe('2.00 KiB')
    expect(wrapper.get('[data-testid="detail-retention"]').text()).toContain('小时总量保留 3 年')
    wrapper.unmount()
  })

  it('shows NIC TX as the headline, keeps the boundary Chinese, and separates setup tabs', async () => {
    const states = reactive(emptyTrafficReviewStates())
    states.summary.data = { txBytes: 1024 ** 3, rxBytes: 99 * 1024 ** 3, proxyUploadBytes: 90 * 1024 ** 3, lastSampleAt: 1000, networkSampleCount: 1, proxySampleCount: 1, toRemoteEgressBytes: 20, toClientEgressBytes: 40, disclaimer: 'Local byte measurements are not Google billing classification.' }
    states.destinations.data = [{ layer: 'network', peer: '203.0.113.1', txBytes: 10, leg: 'to_client' }]
    states.health.data = { enabled: true, available: false, networkInterface: 'ens4', conntrack: { available: false, degraded: true, lastError: 'permission denied' } }
    states.alerts.data = [{ id: 1, kind: 'unknown_backend_event', message: 'An untranslated backend error', time: 1000 }]
    const stop = vi.fn()
    vi.mocked(createTrafficReviewPoller).mockReturnValue({ states, start: vi.fn(), stop, refresh: vi.fn(), setQuery: vi.fn(), isLoading: () => false } as unknown as ReturnType<typeof createTrafficReviewPoller>)
    const wrapper = mount(TrafficReview, { attachTo: document.body, global: { plugins: [createVuetify({ components }), createI18n({ legacy: false, locale: 'zhHans', messages: { zhHans: { pages: { trafficReview: '出站流量' }, trafficReview: trafficReviewZh } } })] } })
    await flushPromises()
    expect(wrapper.get('[data-testid="nic-egress"]').text()).toBe('1.000 GiB')
    expect(wrapper.text()).toContain('响应入站对端的出站')
    expect(wrapper.text()).not.toContain('Local byte measurements')
    const tabs = wrapper.findAll('[role="tab"]')
    expect(tabs.map(tab => tab.text())).toEqual(['出站总览', 'GCP 账单', '中转配置', '采集诊断'])
    await tabs[1]!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('尚未配置')
    expect(wrapper.text()).not.toContain('USD 0')
    await tabs[2]!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('中转不保证降低 GCP 费用')
    await tabs[3]!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('permission denied')
    expect(wrapper.text()).not.toContain('An untranslated backend error')
    expect(wrapper.text()).toContain('连接采集权限不足')
    expect(wrapper.text()).toContain('新采集已排除这两类误报')
    wrapper.unmount()
    expect(stop).toHaveBeenCalledOnce()
  })
})
