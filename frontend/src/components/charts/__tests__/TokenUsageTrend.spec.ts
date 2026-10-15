import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import TokenUsageTrend from '../TokenUsageTrend.vue'

const messages: Record<string, string> = {
  'admin.dashboard.tokenUsageTrend': 'Token Usage Trend',
  'admin.dashboard.noDataAvailable': 'No data available',
  'admin.dashboard.originalTokenTrendMissing': 'Original usage is missing for some periods',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-chartjs', () => ({
  Line: {
    name: 'Line',
    props: ['data', 'options'],
    template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>',
  },
}))

describe('TokenUsageTrend', () => {
  it('calculates cache hit rate against all prompt tokens', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 500,
            output_tokens: 100,
            cache_creation_tokens: 0,
            cache_read_tokens: 1500,
            total_tokens: 2100,
            original_usage_requests: 1,
            original_usage: { input_tokens: 500, output_tokens: 100, cache_creation_tokens: 0, cache_read_tokens: 1500 },
            cost: 0.01,
            actual_cost: 0.005,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === 'Cache Hit Rate'
    )
    // Hit rate = 1500 / (500 + 1500 + 0) * 100 = 75%
    expect(hitRateDataset.data[0]).toBe(75)
  })

  it('returns 0 hit rate when all prompt tokens are zero', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 0,
            input_tokens: 0,
            output_tokens: 0,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            total_tokens: 0,
            cost: 0,
            actual_cost: 0,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === 'Cache Hit Rate'
    )
    expect(hitRateDataset.data[0]).toBe(0)
  })

  it('includes cache_creation_tokens in denominator for Anthropic models', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 200,
            output_tokens: 50,
            cache_creation_tokens: 300,
            cache_read_tokens: 500,
            total_tokens: 1050,
            original_usage_requests: 1,
            original_usage: { input_tokens: 200, output_tokens: 50, cache_creation_tokens: 300, cache_read_tokens: 500 },
            cost: 0.02,
            actual_cost: 0.01,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === 'Cache Hit Rate'
    )
    // Hit rate = 500 / (200 + 500 + 300) * 100 = 50%
    expect(hitRateDataset.data[0]).toBe(50)
  })

  it('plots original tokens and 80% cache rate while retaining converted billing costs', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [{
          date: '2026-10-15 15:00', requests: 2,
          input_tokens: 0, output_tokens: 100, cache_creation_tokens: 0, cache_read_tokens: 9000,
          total_tokens: 9100, cost: 0.01, actual_cost: 0.02,
          original_usage_requests: 2,
          original_usage: { input_tokens: 0, output_tokens: 100, cache_creation_tokens: 2000, cache_read_tokens: 8000 },
        }],
      },
    })
    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const dataset = (label: string) => chartData.datasets.find((ds: any) => ds.label === label)
    expect(dataset('Input').data).toEqual([0])
    expect(dataset('Output').data).toEqual([100])
    expect(dataset('Cache Creation').data).toEqual([2000])
    expect(dataset('Cache Read').data).toEqual([8000])
    expect(dataset('Cache Hit Rate').data).toEqual([80])
    const chart = wrapper.findComponent({ name: 'Line' })
    const options = chart.props('options')
    expect(options.plugins.tooltip.callbacks.footer([{ dataIndex: 0 }])).toContain('Actual: $0.020')
    expect(chartData.labels).toEqual(['2026-10-15 15:00'])
    expect(wrapper.text()).not.toContain(messages['admin.dashboard.originalTokenTrendMissing'])
  })

  it.each([undefined, null, { input_tokens: 0, output_tokens: 0, cache_creation_tokens: 0, cache_read_tokens: 0 }])(
    'leaves missing or incomplete historical original usage blank: %s', (original) => {
      const wrapper = mount(TokenUsageTrend, {
        props: {
          trendData: [{
            date: '2026-10-15 14:00', requests: 2,
            input_tokens: 0, output_tokens: 100, cache_creation_tokens: 0, cache_read_tokens: 9000,
            total_tokens: 9100, cost: 0.01, actual_cost: 0.02,
            original_usage_requests: original ? 1 : 0, original_usage: original,
          }],
        },
      })
      const chartData = JSON.parse(wrapper.find('.chart-data').text())
      for (const dataset of chartData.datasets) expect(dataset.data).toEqual([null])
      expect(wrapper.text()).toContain(messages['admin.dashboard.originalTokenTrendMissing'])
      const options = wrapper.findComponent({ name: 'Line' }).props('options')
      expect(options.plugins.tooltip.callbacks.label({ raw: null })).toBe('admin.dashboard.originalTokenTrendUnavailable')
    }
  )

  it('treats an observed zero cache read as 0%, never as missing', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [{
          date: '2026-10-15 17:00', requests: 1,
          input_tokens: 10, output_tokens: 20, cache_creation_tokens: 0, cache_read_tokens: 500,
          total_tokens: 530, cost: 0.01, actual_cost: 0.02,
          original_usage_requests: 1,
          original_usage: { input_tokens: 10, output_tokens: 20, cache_creation_tokens: 1000, cache_read_tokens: 0 },
        }],
      },
    })
    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    expect(chartData.datasets.find((ds: any) => ds.label === 'Cache Hit Rate').data).toEqual([0])
    expect(wrapper.text()).not.toContain(messages['admin.dashboard.originalTokenTrendMissing'])
  })
})
