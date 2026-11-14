import { act, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { BillingHistoryDialog } from '@/features/wallet/components/dialogs/billing-history-dialog'
import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { mapStatusDataToConfig } from '@/lib/status-query'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { CnyReference, CnyReferenceNote } from '../cny-reference'

const resources = { en, zhCN: zh, zhTW, fr, ja, ru, vi: viLocale }
const note =
  'CNY estimates use the platform display rate for reference only. Actual payment is determined by the payment provider.'
function status(rate: unknown = 7.25, type = 'CNY') {
  act(() =>
    useSystemConfigStore.getState().setConfig(
      mapStatusDataToConfig({
        quota_display_type: type,
        usd_exchange_rate: rate,
      })
    )
  )
}
beforeEach(() => {
  localStorage.clear()
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  status()
})
afterEach(() => vi.restoreAllMocks())

it.each([
  ['en', '72.5'],
  ['zhCN', '72.5'],
  ['zhTW', '72.5'],
  ['fr', '72,5'],
  ['ja', '72.5'],
  ['ru', '72,5'],
  ['vi', '72,5'],
])(
  'localizes references and both new messages in %s and responds to language changes',
  async (language, number) => {
    const i18n = createInstance()
    await i18n.init({
      lng: language,
      resources,
      fallbackLng: 'en',
      interpolation: { escapeValue: false },
    })
    render(
      <I18nextProvider i18n={i18n}>
        <CnyReference usd={10} />
        <CnyReferenceNote />
      </I18nextProvider>
    )
    expect(screen.getByText(`≈ ¥${number}`)).toBeVisible()
    expect(
      screen.getByText(
        resources[language as keyof typeof resources].translation[note]
      )
    ).toBeVisible()
    status('invalid')
    expect(
      screen.getByText(
        resources[language as keyof typeof resources].translation[
          'CNY reference unavailable'
        ]
      )
    ).toBeVisible()
    status()
    await act(() => i18n.changeLanguage(language === 'fr' ? 'en' : 'fr'))
    expect(
      screen.getByText(language === 'fr' ? '≈ ¥72.5' : '≈ ¥72,5')
    ).toBeVisible()
  }
)
it.each([
  undefined,
  null,
  0,
  -1,
  '',
  ' ',
  'bad',
  Number.NaN,
  Infinity,
  'Infinity',
])('shows unavailable for raw rate %s', (rate) => {
  status(rate === undefined ? undefined : rate)
  if (rate === undefined) {
    act(() =>
      useSystemConfigStore
        .getState()
        .setConfig(mapStatusDataToConfig({ quota_display_type: 'CNY' }))
    )
  }
  render(<CnyReference usd={10} />)
  expect(screen.getByText('CNY reference unavailable')).toBeVisible()
})
it.each(['USD', 'TOKENS', 'CUSTOM'])(
  'omits references and note in %s',
  (type) => {
    status(7.25, type)
    const view = render(
      <>
        <CnyReference usd={10} />
        <CnyReferenceNote />
      </>
    )
    expect(view.container).toBeEmptyDOMElement()
  }
)
it.each([
  [Number.MAX_VALUE, 8],
  [Number.MIN_VALUE, 0.1],
])('shows unavailable for overflow or underflow %s × %s', (usd, rate) => {
  status(rate)
  render(<CnyReference usd={usd} />)
  expect(screen.getByText('CNY reference unavailable')).toBeVisible()
})
it.each([0.0002, 1e-30, Number.MIN_VALUE])(
  'preserves positive tiny reference %s',
  (usd) => {
    status(1)
    render(<CnyReference usd={usd} />)
    const reference = screen.getByText(/^≈ ¥/)
    expect(reference.textContent).not.toBe('≈ ¥0')
    expect(reference.textContent).not.toBe('≈ ¥0.0001')
  }
)
it('does not trust a legacy persisted rate without provenance', async () => {
  localStorage.setItem(
    'system-config-storage',
    JSON.stringify({
      state: {
        config: {
          systemName: 'Test',
          logo: '',
          currency: {
            ...DEFAULT_CURRENCY_CONFIG,
            quotaDisplayType: 'CNY',
            usdExchangeRate: 7.25,
          },
        },
      },
      version: 0,
    })
  )
  await useSystemConfigStore.persist.rehydrate()
  render(<CnyReference usd={10} />)
  expect(screen.getByText('CNY reference unavailable')).toBeVisible()
})
it('renders historical USD snapshots with the current rate and keeps history recovery identity', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        total: 1,
        items: [
          {
            id: 1,
            trade_no: 'old-order',
            status: 'pending',
            payment_method: 'idatariver',
            create_time: 1,
            money: 999,
            amount: 999,
            idatariver: { price: 10, quota: 12 },
          },
        ],
      },
    },
  })
  const resume = vi.fn()
  render(
    <BillingHistoryDialog
      open
      onOpenChange={() => {}}
      onResumeIdataRiver={resume}
    />
  )
  expect(await screen.findByText('10 USD')).toBeVisible()
  const credit = screen.getByText('12 USD').parentElement
  if (!credit) throw new Error('Missing historical credit region')
  expect(within(credit).getByText('≈ ¥87')).toBeVisible()
  expect(screen.getByText('≈ ¥72.5')).toBeVisible()
  status(8)
  expect(screen.getByText('≈ ¥80')).toBeVisible()
  expect(screen.getByText('≈ ¥96')).toBeVisible()
  expect(screen.getByText('10 USD')).toBeVisible()
  screen.getByRole('button', { name: 'Check Status' }).click()
  expect(resume).toHaveBeenCalledExactlyOnceWith('old-order')
  expect(
    get.mock.calls.every(([url]) =>
      String(url).startsWith('/api/user/topup/self?')
    )
  ).toBe(true)
})
