import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CustomRoutingDialog } from '../custom-routing-dialog'
import type { CustomRouting, RoutingCandidate } from '../types'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let client: QueryClient
afterEach(() => client?.clear())

const candidates: RoutingCandidate[] = ['standard', 'priority', 'offline'].map(
  (group) => ({
    model: 'chat-model',
    display_name: 'Chat model',
    category: 'text',
    group,
    available: true,
    health: group === 'offline' ? 'soft_offline' : 'normal',
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
  })
)
const empty: CustomRouting = {
  schema_version: 1,
  mode: 'smart',
  strategy: 'custom',
  models: {},
}

function mount(
  value: CustomRouting = empty,
  available: RoutingCandidate[] | Error = candidates,
  keyConfiguration = false
) {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (available instanceof Error) throw available
    return {
      data: {
        success: true,
        data: {
          items: url.endsWith('/models')
            ? [
                ...new Map(
                  available.map((item) => [
                    item.model,
                    {
                      ...item,
                      group_count: available.filter(
                        (other) => other.model === item.model
                      ).length,
                    },
                  ])
                ).values(),
              ]
            : available,
          total: available.length,
          page: 1,
          page_size: 100,
        },
      },
    }
  })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onSave = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <CustomRoutingDialog
          keyConfiguration={keyConfiguration}
          value={value}
          onSave={onSave}
          onClose={() => undefined}
        />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return onSave
}

test('scheduling step shows its own title, reference strategy order and recommendation, and contextual group summaries', async () => {
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['standard', 'priority'],
        strategy: 'balanced',
        try_soft_offline_groups: [],
      },
    },
  })
  const user = userEvent.setup()
  expect(
    screen.getByRole('dialog', { name: 'Custom model groups' })
  ).toBeVisible()
  await user.click(
    await screen.findByRole('button', {
      name: 'Next: Scheduling configuration',
    })
  )
  expect(
    screen.getByRole('dialog', { name: 'Scheduling configuration' })
  ).toBeVisible()
  const controls = within(
    screen.getByRole('group', { name: 'Apply scheduling to all models' })
  )
  expect(
    controls
      .getAllByRole('button')
      .map((button) => button.getAttribute('aria-label'))
  ).toEqual([
    'Balanced',
    'Success first',
    'Speed first',
    'Price first',
    'Manual order',
  ])
  expect(
    within(controls.getByRole('button', { name: 'Balanced' })).getByText(
      'Recommended'
    )
  ).toBeVisible()
  expect(controls.getByRole('button', { name: 'Balanced' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getByText('2 groups · Balanced')).toBeVisible()
  await user.click(controls.getByRole('button', { name: 'Success first' }))
  expect(
    controls.getByRole('button', { name: 'Success first' })
  ).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByText('2 groups · Success first')).toBeVisible()
})

test('only Key configuration asks users to save the Key after applying', async () => {
  mount(empty, candidates, true)
  expect(
    await screen.findByText(
      'After applying, save the Key for these settings to take effect.'
    )
  ).toBeVisible()
})

test('healthy selection survives filters, manual keyboard reorder preserves focus, and apply emits only the draft', async () => {
  const user = userEvent.setup()
  const onSave = mount()
  await user.click(await screen.findByRole('button', { name: 'chat-model' }))
  await user.click(
    screen.getByRole('button', { name: 'Select healthy groups on this page' })
  )
  expect(
    screen.getByRole('checkbox', { name: /^Select chat-model \/ offline/ })
  ).not.toBeChecked()
  await user.type(
    screen.getByRole('textbox', { name: 'Search model name' }),
    'chat'
  )
  expect(
    screen.getByRole('button', { name: 'Remove chat-model / standard' })
  ).toBeInTheDocument()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Next: Scheduling configuration' })
    ).toBeEnabled()
  )
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  await user.click(screen.getByRole('button', { name: 'Manual order' }))
  const handle = screen.getByRole('button', {
    name: 'Drag standard to reorder',
  })
  handle.focus()
  fireEvent.keyDown(handle, { key: 'ArrowDown' })
  expect(handle).toHaveFocus()
  expect(
    screen.getByRole('button', { name: 'Move standard down' })
  ).toBeDisabled()
  await user.click(
    screen.getByRole('switch', {
      name: 'Try chat-model / standard while temporarily offline',
    })
  )
  await user.click(
    screen.getByRole('button', { name: 'Use this configuration' })
  )
  expect(onSave).toHaveBeenCalledWith({
    ...empty,
    models: {
      'chat-model': {
        groups: ['priority', 'standard'],
        strategy: 'manual',
        try_soft_offline_groups: ['standard'],
      },
    },
  })
})

test('removing the last saved group removes its model and disables continuation', async () => {
  const user = userEvent.setup()
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['standard'],
        strategy: 'balanced',
        try_soft_offline_groups: [],
      },
    },
  })
  await user.click(
    await screen.findByRole('button', { name: 'Remove chat-model / standard' })
  )
  expect(
    screen.queryByRole('button', { name: 'Remove chat-model / standard' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  ).toBeDisabled()
})

test('a revoked saved group remains visible and must be removed before advancing', async () => {
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['revoked', 'standard'],
        strategy: 'manual',
        try_soft_offline_groups: [],
      },
    },
  })
  await screen.findByRole('alert')
  expect(
    screen.getByRole('button', { name: 'Remove chat-model / revoked' })
  ).toHaveTextContent('Unavailable')
  expect(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  ).toBeDisabled()
})

test('candidate loading failure exposes retry and keeps the draft unsaved', async () => {
  const onSave = mount(empty, new Error('offline'))
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(onSave).not.toHaveBeenCalled()
})

test('custom scope and single-group warning follow selection count', async () => {
  mount()
  const user = userEvent.setup()
  expect(
    screen.queryByText(
      'After applying, save the Key for these settings to take effect.'
    )
  ).not.toBeInTheDocument()
  expect(
    screen.getByText('Only models and groups selected here will be used.')
  ).toBeVisible()
  await user.click(
    await screen.findByRole('checkbox', {
      name: 'Select chat-model / standard',
    })
  )
  expect(
    screen.getByText(
      'Only one group is selected. If it fails, there is no other selected group to try.'
    )
  ).toBeVisible()
  await user.click(
    screen.getByRole('checkbox', { name: 'Select chat-model / priority' })
  )
  expect(
    screen.queryByText(/Only one group is selected/)
  ).not.toBeInTheDocument()
})

test('custom health-empty clears every filter without losing selected groups', async () => {
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['standard'],
        strategy: 'balanced',
        try_soft_offline_groups: [],
      },
    },
  })
  const user = userEvent.setup()
  await screen.findByRole('checkbox', { name: 'Select chat-model / standard' })
  await user.click(screen.getByRole('combobox', { name: 'Health filter' }))
  await user.click(await screen.findByRole('option', { name: 'Congested' }))
  await user.click(await screen.findByRole('button', { name: 'Clear filters' }))
  expect(
    await screen.findByRole('checkbox', {
      name: 'Select chat-model / standard',
    })
  ).toBeChecked()
  expect(screen.getByRole('button', { name: 'chat-model' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(
    screen.getByRole('combobox', { name: 'Health filter' })
  ).toHaveTextContent('All')
})

test('custom true-empty explains authorization and reloads when candidates become available', async () => {
  mount(empty, [])
  expect(
    await screen.findByText(/No groups are available for this account or Key/)
  ).toBeVisible()
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items: url.endsWith('/models')
          ? [
              {
                model: 'chat-model',
                display_name: 'Chat',
                category: 'text',
                group_count: 3,
              },
            ]
          : candidates,
        total: 3,
        page: 1,
        page_size: 100,
      },
    },
  }))
  await userEvent
    .setup()
    .click(screen.getByRole('button', { name: 'Reload candidates' }))
  await screen.findByRole('checkbox', { name: 'Select chat-model / standard' })
})

test('current model scheduling explanation stays visible and follows individual, batch and back navigation changes', async () => {
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['standard', 'priority'],
        strategy: 'balanced',
        try_soft_offline_groups: [],
      },
    },
  })
  const user = userEvent.setup()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Next: Scheduling configuration' })
    ).toBeEnabled()
  )
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  expect(
    screen.getByText(/Balances price, speed and reliability/)
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Price first' }))
  expect(
    screen.getByText(/Prefers lower-priced groups/, { selector: 'p' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Manual order' }))
  expect(
    screen.getByText(
      'Tries groups from top to bottom, skipping unavailable ones. Drag or use the up and down buttons to reorder.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Previous step' }))
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  expect(screen.getByText(/Tries groups from top to bottom/)).toBeVisible()
})

test('complete model navigation, one strategy and independent group preferences survive back and removal', async () => {
  const initial: CustomRouting = {
    ...empty,
    models: {
      'chat-model': {
        groups: ['standard', 'priority'],
        strategy: 'balanced',
        try_soft_offline_groups: [],
      },
      'other-model': {
        groups: ['standard'],
        strategy: 'balanced',
        try_soft_offline_groups: [],
      },
    },
  }
  const snapshot = JSON.stringify(initial)
  const onSave = mount(initial, [
    ...candidates,
    { ...candidates[0], model: 'other-model', display_name: 'Other model' },
  ])
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'other-model' }))
  expect(screen.getByRole('button', { name: 'chat-model' })).toBeVisible()
  await user.click(
    screen.getByRole('switch', {
      name: 'Try chat-model / priority while temporarily offline',
    })
  )
  expect(
    screen.getByRole('switch', {
      name: 'Try chat-model / standard while temporarily offline',
    })
  ).not.toBeChecked()
  expect(
    screen.getByRole('switch', {
      name: 'Try other-model / standard while temporarily offline',
    })
  ).not.toBeChecked()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Next: Scheduling configuration' })
    ).toBeEnabled()
  )
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  expect(
    screen.queryByRole('combobox', { name: /Scheduling for/ })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Speed first' }))
  await user.click(screen.getByRole('button', { name: 'Previous step' }))
  expect(
    screen.getByRole('switch', {
      name: 'Try chat-model / priority while temporarily offline',
    })
  ).toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Remove chat-model / priority' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  expect(screen.getByRole('button', { name: 'Speed first' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  await user.click(
    screen.getByRole('button', { name: 'Use this configuration' })
  )
  expect(onSave).toHaveBeenCalledWith({
    ...initial,
    models: {
      'chat-model': {
        groups: ['standard'],
        strategy: 'speed',
        try_soft_offline_groups: [],
      },
      'other-model': {
        groups: ['standard'],
        strategy: 'speed',
        try_soft_offline_groups: [],
      },
    },
  })
  expect(JSON.stringify(initial)).toBe(snapshot)
})
