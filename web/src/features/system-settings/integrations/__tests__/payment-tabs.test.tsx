import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PaymentSettingsSection } from '../payment-settings-section'

afterEach(() => vi.restoreAllMocks())

it('keeps General first and selected by default, with iDataRiver last and reachable by click and keyboard', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  const put = vi.spyOn(api, 'put')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <PaymentSettingsSection
        defaultValues={{
          PayAddress: '',
          EpayId: '',
          EpayKey: '',
          Price: 1,
          MinTopUp: 1,
          CustomCallbackAddress: '',
          PayMethods: '[]',
          AmountOptions: '[]',
          AmountDiscount: '{}',
          StripeApiSecret: '',
          StripeWebhookSecret: '',
          StripePriceId: '',
          StripeUnitPrice: 1,
          StripeMinTopUp: 1,
          StripePromotionCodesEnabled: false,
          CreemApiKey: '',
          CreemWebhookSecret: '',
          CreemTestMode: false,
          CreemProducts: '[]',
        }}
        waffoDefaultValues={{
          WaffoEnabled: false,
          WaffoApiKey: '',
          WaffoPrivateKey: '',
          WaffoPublicCert: '',
          WaffoSandboxPublicCert: '',
          WaffoSandboxApiKey: '',
          WaffoSandboxPrivateKey: '',
          WaffoSandbox: false,
          WaffoMerchantId: '',
          WaffoCurrency: 'USD',
          WaffoUnitPrice: 1,
          WaffoMinTopUp: 1,
          WaffoNotifyUrl: '',
          WaffoReturnUrl: '',
          WaffoPayMethods: '[]',
        }}
        waffoPancakeDefaultValues={{
          WaffoPancakeMerchantID: '',
          WaffoPancakePrivateKey: '',
          WaffoPancakeReturnURL: '',
        }}
        complianceDefaults={{
          confirmed: true,
          termsVersion: 'v1',
          confirmedAt: 1,
          confirmedBy: 1,
        }}
      />
    </QueryClientProvider>
  )
  try {
    const tabs = within(screen.getByRole('tablist'))
    expect(tabs.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'General',
      'Epay',
      'Stripe',
      'Creem',
      'Waffo Pancake',
      'Waffo',
      'iDataRiver',
    ])
    const general = tabs.getByRole('tab', { name: 'General' })
    const idr = tabs.getByRole('tab', { name: 'iDataRiver' })
    expect(general).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.getByText('Shared configuration for all payment gateways')
    ).toBeVisible()
    const user = userEvent.setup()
    await user.click(idr)
    expect(idr).toHaveAttribute('aria-selected', 'true')
    expect(
      await screen.findByRole('switch', { name: 'Enable iDataRiver' })
    ).toBeVisible()
    await user.keyboard('{ArrowLeft}')
    expect(tabs.getByRole('tab', { name: /^Waffo$/ })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(idr).toHaveFocus()
    await user.keyboard('{Home}')
    expect(general).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(general).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{End}{Enter}')
    expect(idr).toHaveFocus()
    expect(idr).toHaveAttribute('aria-selected', 'true')
    expect(put).not.toHaveBeenCalled()
  } finally {
    view.unmount()
    client.clear()
  }
})
