import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import ProxyPoolCapacityPreview from '../ProxyPoolCapacityPreview.vue'
import resources from '@/i18n/locales/zh/admin/resources'

const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh: { admin: resources } } })

describe('strict priority capacity preview', () => {
  it.each([
    [10, 100, 10, 10, 1, 10],
    [20, 100, 10, 10, 2, 10],
    [30, 100, 10, 10, 3, 10],
    [25, 100, 10, 10, 3, 5],
    [20, 105, 10, 10, 2, 10],
    [10, 5, 10, 1, 10, 1],
    [5, 100, 10, 5, 1, 5],
  ])('previews pool %s / account %s / IP %s without exceeding pool capacity', (poolSize, accountConcurrency, proxyConcurrency, ips, accounts, last) => {
    const wrapper = mount(ProxyPoolCapacityPreview, { props: { poolSize, accountConcurrency, proxyConcurrency }, global: { plugins: [i18n] } })
    expect(wrapper.get('p').text()).toContain(`每账号最多 ${ips} 个，最多供前 ${accounts} 个可调度账号同时使用，末位分配 ${last} 个 IP`)
    wrapper.unmount()
  })

  it('hides a capacity claim while concurrency input is invalid', () => {
    const wrapper = mount(ProxyPoolCapacityPreview, { props: { poolSize: 20, accountConcurrency: 0, proxyConcurrency: 10 }, global: { plugins: [i18n] } })
    expect(wrapper.find('[data-testid="proxy-pool-capacity"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
