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
  it('shows NIC TX as the headline, keeps the boundary Chinese, and separates setup tabs', async () => {
    const states = reactive(emptyTrafficReviewStates())
    states.summary.data = { txBytes: 1024 ** 3, rxBytes: 99 * 1024 ** 3, proxyUploadBytes: 90 * 1024 ** 3, lastSampleAt: 1000, toRemoteEgressBytes: 20, toClientEgressBytes: 40, disclaimer: 'Local byte measurements are not Google billing classification.' }
    states.destinations.data = [{ layer: 'network', peer: '203.0.113.1', txBytes: 10, leg: 'to_client' }]
    states.health.data = { enabled: true, available: false, networkInterface: 'ens4', conntrack: { available: false, degraded: true, lastError: 'permission denied' } }
    states.alerts.data = [{ id: 1, kind: 'unknown_backend_event', message: 'An untranslated backend error', time: 1000 }]
    const stop = vi.fn()
    vi.mocked(createTrafficReviewPoller).mockReturnValue({ states, start: vi.fn(), stop, refresh: vi.fn(), setQuery: vi.fn(), isLoading: () => false } as unknown as ReturnType<typeof createTrafficReviewPoller>)
    const wrapper = mount(TrafficReview, { attachTo: document.body, global: { plugins: [createVuetify({ components }), createI18n({ legacy: false, locale: 'zhHans', messages: { zhHans: { pages: { trafficReview: '出站流量' }, trafficReview: trafficReviewZh } } })] } })
    await flushPromises()
    expect(wrapper.get('[data-testid="nic-egress"]').text()).toBe('1.000 GiB')
    expect(wrapper.text()).toContain('发回客户端')
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
    wrapper.unmount()
    expect(stop).toHaveBeenCalledOnce()
  })
})
