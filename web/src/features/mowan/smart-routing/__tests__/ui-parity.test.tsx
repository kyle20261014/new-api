import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useState } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RoutingControl } from '../routing-control'
import type { DynamicRouting, RoutingCandidate, RoutingPolicy } from '../types'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let client: QueryClient
afterEach(() => client?.clear())
const initial: DynamicRouting = {
  schema_version: 1,
  mode: 'smart',
  strategy: 'balanced',
  excluded_models: ['retired-model'],
  excluded_groups: ['retired-global'],
  excluded_model_groups: { 'retired-model': ['retired-pair'] },
}
const candidate: RoutingCandidate = {
  model: 'chat',
  display_name: 'Chat',
  category: 'text',
  group: 'standard',
  available: true,
  health: 'normal',
  quote: { currency: 'USD', comparison: 'unavailable', prices: [] },
  metrics: {
    protocol_class: 'openai',
    response_kind: 'ttft',
    response_ms_1h: null,
    response_samples_1h: 0,
    success_24h: null,
    samples_24h: 0,
    stale: true,
    updated_at: null,
  },
}
function mount(value: RoutingPolicy | null = initial) {
  vi.spyOn(api, 'post')
  vi.spyOn(api, 'put')
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [candidate],
        total: 1,
        page: 1,
        page_size: 100,
      },
    },
  })
  const changed = vi.fn()
  function Harness() {
    const [draft, setDraft] = useState(value)
    return (
      <RoutingControl
        value={draft}
        onChange={(next) => {
          setDraft(next)
          changed(next)
        }}
      />
    )
  }
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <Harness />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return changed
}
test('strategy choices keep their order and help opens a named dialog without changing the draft', async () => {
  const changed = mount()
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
  expect(
    (await screen.findAllByRole('option')).map(
      (option) => option.getAttribute('aria-label') || option.textContent
    )
  ).toEqual([
    'Custom per model',
    'Balanced',
    'Price first',
    'Speed first',
    'Success first',
  ])
  await user.keyboard('{Escape}')
  const trigger = screen.getByRole('button', { name: 'How to choose?' })
  await user.click(trigger)
  const help = await screen.findByRole('dialog', {
    name: 'How should I choose a routing strategy?',
  })
  expect(
    within(help).getByText(
      /Speed uses successful calls from the last hour; success rate uses call records from the last 24 hours/
    )
  ).toBeInTheDocument()
  expect(
    within(help).getByRole('heading', {
      name: 'Which groups will the system use?',
    })
  ).toBeVisible()
  expect(
    within(help).getByText(/Newly available groups join automatically/)
  ).toBeVisible()
  expect(
    within(help).getByText(/Unknown prices do not mean free/)
  ).toBeVisible()
  expect(
    within(help).getByText(/Cards show timing from the last hour/)
  ).toHaveTextContent('Submitting a task does not mean it is complete.')
  await user.click(within(help).getByRole('button', { name: 'Close' }))
  await waitFor(() => expect(help).not.toBeInTheDocument())
  await waitFor(() => expect(trigger).toHaveFocus())
  expect(changed).not.toHaveBeenCalled()
})

test('speed guidance uses one hour and preserves the 24-hour reliability window', async () => {
  mount({ ...initial, strategy: 'speed' })
  expect(
    screen.getByText('Prefers groups that have responded faster recently.', {
      selector: 'p',
    })
  ).toBeVisible()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'How to choose?' }))
  const help = await screen.findByRole('dialog', {
    name: 'How should I choose a routing strategy?',
  })
  expect(
    within(help).getByText(
      /Speed uses successful calls from the last hour; success rate uses call records from the last 24 hours/
    )
  ).toBeVisible()
  expect(
    within(help).getByText(/Cards show timing from the last hour/)
  ).toBeVisible()
  await user.click(within(help).getByRole('button', { name: 'Close' }))
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  await screen.findByRole('checkbox', { name: 'Select chat / standard' })
  expect(screen.getByTitle('Time to start replying · Last hour')).toBeVisible()
})

test('whole-model restore removes hidden and invalid pairs across pages without changing other models or global rules', async () => {
  const value: DynamicRouting = {
    ...initial,
    excluded_models: ['chat', 'other'],
    excluded_groups: ['standard'],
    excluded_model_groups: {
      chat: ['standard', 'page-two', 'revoked'],
      other: ['keep'],
    },
  }
  const changed = mount(value)
  vi.mocked(api.get).mockImplementation(async (_url, config) => {
    const params = config?.params as { p?: number; search?: string }
    return {
      data: {
        success: true,
        data: {
          items: params.search
            ? []
            : [
                {
                  ...candidate,
                  group: params.p === 2 ? 'page-two' : 'standard',
                },
              ],
          total: params.search ? 0 : 101,
          page: params.p ?? 1,
          page_size: 100,
        },
      },
    }
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  await screen.findByRole('checkbox', { name: 'Select chat / standard' })
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByRole('checkbox', { name: 'Select chat / page-two' })
  await user.type(
    screen.getByRole('textbox', { name: 'Search model name' }),
    'hidden'
  )
  await user.click(
    screen.getByRole('button', { name: 'Restore all exclusions for chat' })
  )
  expect(
    screen.getByRole('button', { name: 'Restore global group standard' })
  ).toBeVisible()
  expect(
    screen.getByText(/Group exclusions that apply to all models stay in place/)
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Apply exclusions' }))
  expect(changed).toHaveBeenLastCalledWith({
    ...value,
    excluded_models: ['other'],
    excluded_model_groups: { other: ['keep'] },
  })
})

test('dynamic search empty clears filters and true empty offers reload without discarding exclusions', async () => {
  mount()
  let noCandidates = false
  vi.mocked(api.get).mockImplementation(async (_url, config) => {
    const params = config?.params as { search?: string }
    const items = noCandidates || params.search ? [] : [candidate]
    return {
      data: {
        success: true,
        data: { items, total: items.length, page: 1, page_size: 100 },
      },
    }
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  await screen.findByRole('checkbox', { name: 'Select chat / standard' })
  await user.type(
    screen.getByRole('textbox', { name: 'Search model name' }),
    'missing'
  )
  await user.click(await screen.findByRole('button', { name: 'Clear filters' }))
  await screen.findByRole('checkbox', { name: 'Select chat / standard' })
  expect(
    screen.getByRole('button', { name: 'Restore model retired-model' })
  ).toBeVisible()
  noCandidates = true
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  client.clear()
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  expect(
    await screen.findByText(/No groups are available for this account or Key/)
  ).toBeVisible()
  noCandidates = false
  await user.click(screen.getByRole('button', { name: 'Reload candidates' }))
  await screen.findByRole('checkbox', { name: 'Select chat / standard' })
})
test('exclusion changes are isolated until save; unknown saved rules remain visible and removable', async () => {
  const changed = mount()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByRole('checkbox', {
    name: 'Select chat / standard',
  })
  expect(within(dialog).getByRole('status')).toHaveTextContent(
    '3 exclusions set; 1 / 1 items kept on this page.'
  )
  await user.click(
    await within(dialog).findByRole('checkbox', {
      name: 'Select chat / standard',
    })
  )
  expect(changed).not.toHaveBeenCalled()
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  expect(
    await screen.findByRole('checkbox', { name: 'Select chat / standard' })
  ).toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Restore global group retired-global' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Restore retired-model / retired-pair' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Restore model retired-model' })
  )
  await user.click(screen.getByRole('button', { name: 'Apply exclusions' }))
  expect(changed).toHaveBeenLastCalledWith({
    ...initial,
    excluded_models: [],
    excluded_groups: [],
    excluded_model_groups: {},
  })
  expect(api.post).not.toHaveBeenCalled()
  expect(api.put).not.toHaveBeenCalled()
})

test('page exclusion actions preserve model and global rules, hidden pairs, and selections on other pages', async () => {
  const value: DynamicRouting = {
    ...initial,
    excluded_models: ['blocked'],
    excluded_groups: ['global'],
    excluded_model_groups: { chat: ['hidden'] },
  }
  const changed = mount(value)
  vi.mocked(api.get).mockImplementation(async (_url, config) => {
    if (_url.endsWith('/models')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              {
                model: 'chat',
                display_name: 'Chat',
                category: 'text',
                group_count: 3,
              },
              {
                model: 'blocked',
                display_name: 'Blocked',
                category: 'text',
                group_count: 1,
              },
            ],
          },
        },
      }
    }
    const params = config?.params as { p?: number }
    const page = params.p ?? 1
    return {
      data: {
        success: true,
        data: {
          items:
            page === 1
              ? [
                  candidate,
                  { ...candidate, group: 'global' },
                  { ...candidate, model: 'blocked' },
                ]
              : [{ ...candidate, group: 'premium' }],
          total: 101,
          page,
          page_size: 100,
        },
      },
    }
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  expect(
    await screen.findByRole('checkbox', { name: 'Select chat / global' })
  ).toHaveAttribute('aria-disabled', 'true')
  await user.click(screen.getByRole('button', { name: 'Exclude this page' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Everything on this page is excluded'
  )
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByRole('checkbox', { name: 'Select chat / premium' })
  await user.click(screen.getByRole('button', { name: 'Exclude this page' }))
  await user.click(screen.getByRole('button', { name: 'Restore this page' }))
  await user.click(screen.getByRole('button', { name: 'Apply exclusions' }))
  expect(changed).toHaveBeenLastCalledWith({
    ...value,
    excluded_model_groups: { chat: ['hidden', 'standard'] },
  })
})

test('custom cancel does not change the selected strategy; both steps keep scheduling and model health settings', async () => {
  const changed = mount()
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
  await user.click(
    await screen.findByRole('option', { name: 'Custom per model' })
  )
  await user.click(await screen.findByRole('button', { name: 'Cancel' }))
  expect(changed).not.toHaveBeenCalled()
  expect(
    screen.getByRole('combobox', { name: 'Routing strategy' })
  ).toHaveTextContent('Balanced')
  await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
  await user.click(
    await screen.findByRole('option', { name: 'Custom per model' })
  )
  await user.click(
    await screen.findByRole('checkbox', { name: 'Select chat / standard' })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Next: Scheduling configuration' })
    ).toBeEnabled()
  )
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  await user.click(screen.getByRole('button', { name: 'Manual order' }))
  await user.click(
    screen.getByRole('switch', {
      name: 'Try chat / standard while temporarily offline',
    })
  )
  await user.click(screen.getByRole('button', { name: 'Previous step' }))
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  expect(screen.getByRole('button', { name: 'Manual order' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(
    screen.getByRole('switch', {
      name: 'Try chat / standard while temporarily offline',
    })
  ).toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Remove standard' }))
  expect(
    screen.getByRole('button', { name: 'Use this configuration' })
  ).toBeDisabled()
  expect(changed).not.toHaveBeenCalled()
})
