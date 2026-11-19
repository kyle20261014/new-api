/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, test, vi } from 'vitest'

import type { ApiKey } from '../../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { ApiKeysProvider } = await import('../api-keys-provider')
const { ApiKeysMutateDrawer } = await import('../api-keys-mutate-drawer')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = {
  get: ApiMethod
  post: ApiMethod
}
type RenderedDrawer = {
  queryClient: InstanceType<typeof QueryClient>
}

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPost = apiClient.post
let renderedDrawer: RenderedDrawer | null = null

function installApiFixtures(createdPayloads: Array<Record<string, unknown>>) {
  apiClient.get = async (url) => {
    switch (url) {
      case '/api/status':
        return { data: { data: { default_use_auto_group: true } } }
      case '/api/user/models':
        return { data: { success: true, data: [] } }
      case '/api/user/self/groups':
        return {
          data: {
            success: true,
            data: {
              auto: { desc: 'Automatic routing', ratio: 'auto' },
              default: { desc: 'Standard access', ratio: 1 },
              vip: { desc: 'Priority access', ratio: 2 },
            },
          },
        }
      case '/api/token/auto-groups':
        return {
          data: {
            success: true,
            data: { groups: ['vip', 'default'], max_count: 3 },
          },
        }
      default:
        throw new Error(`Unexpected GET ${url}`)
    }
  }
  apiClient.post = async (url, data) => {
    expect(url).toBe('/api/token/')
    expect(data && typeof data === 'object').toBeTruthy()
    createdPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
}

async function renderCreateDrawer(
  currentRow?: ApiKey,
  onOpenChange = vi.fn()
): Promise<void> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(
    ['status'],
    { default_use_auto_group: true },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-models'],
    { success: true, data: [] },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-groups'],
    {
      success: true,
      data: {
        auto: { desc: 'Automatic routing', ratio: 'auto' },
        default: { desc: 'Standard access', ratio: 1 },
        vip: { desc: 'Priority access', ratio: 2 },
      },
    },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['token-auto-groups'],
    {
      success: true,
      data: { groups: ['vip', 'default'], max_count: 3 },
    },
    { updatedAt: freshAt }
  )
  renderedDrawer = { queryClient }

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            currentRow={currentRow}
            onOpenChange={onOpenChange}
          />
        </ApiKeysProvider>
      </I18nextProvider>
    </QueryClientProvider>
  )
  await waitFor(
    () => {
      const saveButton = findButton('Save changes', false)
      expect(saveButton).toBeEnabled()
    },
    { timeout: 1500 }
  )
}

function findButton(text: string, required: true): HTMLButtonElement
function findButton(text: string, required: false): HTMLButtonElement | null
function findButton(text: string, required = true): HTMLButtonElement | null {
  const button = screen
    .queryAllByRole<HTMLButtonElement>('button')
    .find((candidate) => candidate.textContent?.includes(text))
  if (required && !button) {
    throw new Error(`Expected button containing "${text}"`)
  }
  return button ?? null
}

function getControlByLabel(labelText: 'Name' | 'Quantity'): HTMLInputElement
function getControlByLabel(labelText: 'Group'): HTMLButtonElement
function getControlByLabel(labelText: 'Auto group order'): HTMLElement
function getControlByLabel(labelText: string): HTMLElement {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  if (!label) {
    throw new Error(`Expected label "${labelText}"`)
  }

  const control =
    label.control ??
    label
      .closest('[data-slot="form-item"]')
      ?.querySelector<HTMLElement>(
        '[data-slot="form-control"], input, textarea, button[role="combobox"], [role="group"]'
      )
  if (!control) {
    throw new Error(`Expected control for label "${labelText}"`)
  }
  return control
}

function changeInput(input: HTMLInputElement, value: string): void {
  fireEvent.input(input, { target: { value } })
}

afterEach(() => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  localStorage.clear()
  if (renderedDrawer) {
    renderedDrawer.queryClient.clear()
    renderedDrawer = null
  }
})

describe('API keys mutate drawer Auto group integration', () => {
  test('help Escape and close restore focus without closing the Key drawer or changing its draft', async () => {
    const user = userEvent.setup()
    const created: Array<Record<string, unknown>> = []
    installApiFixtures(created)
    const onOpenChange = vi.fn()
    await renderCreateDrawer(undefined, onOpenChange)
    changeInput(getControlByLabel('Name'), 'keep this draft')
    await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
    await user.click(await screen.findByRole('option', { name: 'Speed first' }))
    const trigger = screen.getByRole('button', { name: 'How to choose?' })
    trigger.focus()
    await user.keyboard('{Enter}')
    const help = await screen.findByRole('dialog', {
      name: 'How should I choose a routing strategy?',
    })
    await user.tab()
    expect(help.contains(document.activeElement)).toBe(true)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(help).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(screen.getByRole('dialog', { name: 'Create API Key' })).toBeVisible()
    expect(getControlByLabel('Name')).toHaveValue('keep this draft')
    expect(
      screen.getByRole('combobox', { name: 'Routing strategy' })
    ).toHaveTextContent('Speed first')
    await user.click(trigger)
    const reopened = await screen.findByRole('dialog', {
      name: 'How should I choose a routing strategy?',
    })
    await user.click(within(reopened).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(reopened).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(getControlByLabel('Name')).toHaveValue('keep this draft')
    expect(
      screen.getByRole('combobox', { name: 'Routing strategy' })
    ).toHaveTextContent('Speed first')
    expect(created).toEqual([])
  })

  test('a routing version conflict retains the edited name and strategy until an explicit reviewed retry', async () => {
    const user = userEvent.setup()
    installApiFixtures([])
    const original: ApiKey = {
      id: 8,
      name: 'original',
      key: '',
      status: 1,
      remain_quota: 0,
      used_quota: 0,
      unlimited_quota: true,
      expired_time: -1,
      created_time: 1,
      accessed_time: 0,
      group: 'auto',
      auto_groups: null,
      cross_group_retry: true,
      model_limits_enabled: false,
      model_limits: '',
      allow_ips: '',
      allow_user_quota_query: false,
      routing: { schema_version: 1, mode: 'smart', strategy: 'balanced' },
      routing_version: 3,
      allow_routing_self_write: false,
    }
    let latest = original
    const getFixture = apiClient.get
    apiClient.get = async (url) =>
      url === '/api/token/8'
        ? { data: { success: true, data: latest } }
        : getFixture(url)
    const put = vi
      .spyOn(api, 'put')
      .mockRejectedValueOnce({
        response: {
          status: 409,
          data: { success: false, code: 'routing_version_conflict' },
        },
      })
      .mockRejectedValueOnce({
        response: {
          status: 409,
          data: { success: false, code: 'routing_version_conflict' },
        },
      })
      .mockResolvedValue({ data: { success: true } })
    await renderCreateDrawer(original)
    changeInput(getControlByLabel('Name'), 'my draft')
    await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
    await user.click(await screen.findByRole('option', { name: 'Price first' }))
    await user.click(findButton('Save changes', true))
    expect(
      await screen.findByRole('button', { name: 'Copy routing draft' })
    ).toBeEnabled()
    expect(getControlByLabel('Name')).toHaveValue('my draft')
    expect(
      screen.getByRole('combobox', { name: 'Routing strategy' })
    ).toHaveTextContent('Price first')
    latest = { ...original, name: 'server changed', routing_version: 4 }
    await user.click(
      screen.getByRole('button', { name: 'Review latest policy' })
    )
    await user.click(
      await screen.findByRole('button', {
        name: 'Use reviewed version for this draft',
      })
    )
    expect(getControlByLabel('Name')).toHaveValue('my draft')
    await user.click(findButton('Save changes', true))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
    expect(put.mock.calls[1][1]).toMatchObject({
      id: 8,
      name: 'my draft',
      expected_routing_version: 4,
      routing: { mode: 'smart', strategy: 'price' },
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('button', {
          name: 'Use reviewed version for this draft',
        })
      ).not.toBeInTheDocument()
    )
    expect(getControlByLabel('Name')).toHaveValue('my draft')
    latest = { ...latest, routing_version: 5 }
    await user.click(
      screen.getByRole('button', { name: 'Review latest policy' })
    )
    await user.click(
      await screen.findByRole('button', {
        name: 'Use reviewed version for this draft',
      })
    )
    await user.click(findButton('Save changes', true))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(3))
    expect(put.mock.calls[2][1]).toMatchObject({
      name: 'my draft',
      expected_routing_version: 5,
      routing: { strategy: 'price' },
    })
  })

  test('creates every new key with smart balanced routing and self write off', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    expect(
      screen.getByRole('combobox', { name: 'Routing mode' })
    ).toHaveTextContent('Smart routing')
    expect(
      screen.getByRole('switch', { name: 'Allow key to edit its routing' })
    ).not.toBeChecked()

    changeInput(getControlByLabel('Name'), 'batch')
    changeInput(getControlByLabel('Quantity'), '2')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(2))

    expect(createdPayloads.length).toBe(2)
    expect(createdPayloads[0]?.name).toBe('batch')
    for (const payload of createdPayloads) {
      expect(payload.routing).toMatchObject({
        schema_version: 1,
        mode: 'smart',
        strategy: 'balanced',
      })
      expect(payload).not.toHaveProperty('group')
      expect(payload).not.toHaveProperty('auto_groups')
      expect(payload.allow_routing_self_write).toBe(false)
    }
  })

  test('preserves a smart draft after switching to fixed and back', async () => {
    const user = userEvent.setup()
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
    await user.click(await screen.findByRole('option', { name: 'Price first' }))
    await user.click(screen.getByRole('combobox', { name: 'Routing mode' }))
    await user.click(await screen.findByRole('option', { name: 'Fixed group' }))
    await user.click(screen.getByRole('combobox', { name: 'Fixed group' }))
    await user.click(await screen.findByRole('option', { name: 'vip' }))
    await user.click(screen.getByRole('combobox', { name: 'Routing mode' }))
    await user.click(
      await screen.findByRole('option', { name: 'Smart routing' })
    )
    expect(
      screen.getByRole('combobox', { name: 'Routing strategy' })
    ).toHaveTextContent('Price first')

    changeInput(getControlByLabel('Name'), 'custom')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.routing).toMatchObject({ strategy: 'price' })
    expect(createdPayloads[0]?.routing).not.toHaveProperty('fixed_group')
  })
})

describe('API key expiry across editing sessions', () => {
  test.each(['close', 'save'] as const)(
    'keeps an uncached key non-expiring after %s of a key with an expiry',
    async (finish) => {
      installApiFixtures([])
      const first: ApiKey = {
        id: 1,
        name: 'expiring',
        key: '',
        status: 1,
        remain_quota: 0,
        used_quota: 0,
        unlimited_quota: true,
        expired_time: 2000000000,
        created_time: 1,
        accessed_time: 0,
        group: 'default',
        auto_groups: [],
        cross_group_retry: false,
        model_limits_enabled: false,
        model_limits: '',
        allow_ips: '',
        allow_user_quota_query: true,
      }
      const second = { ...first, id: 2, name: 'non-expiring', expired_time: -1 }
      let resolveDetail!: (value: { data: unknown }) => void
      const detail = new Promise<{ data: unknown }>((resolve) => {
        resolveDetail = resolve
      })
      const getFixture = apiClient.get
      const get = vi.spyOn(apiClient, 'get').mockImplementation((url) => {
        if (url === '/api/token/1') {
          return Promise.resolve({ data: { success: true, data: first } })
        }
        if (url === '/api/token/2') return detail
        return getFixture(url)
      })
      const put = vi
        .spyOn(api, 'put')
        .mockResolvedValue({ data: { success: true } })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      renderedDrawer = { queryClient }
      const close = vi.fn()
      const tree = (currentRow: ApiKey, open: boolean) => (
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <ApiKeysProvider>
              <ApiKeysMutateDrawer
                open={open}
                currentRow={currentRow}
                onOpenChange={close}
              />
            </ApiKeysProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
      const view = render(tree(first, true))
      await waitFor(() =>
        expect(findButton('Save changes', true)).toBeEnabled()
      )
      await waitFor(() => expect(screen.getByLabelText('Time')).toBeEnabled())

      if (finish === 'save') {
        fireEvent.click(findButton('Save changes', true))
        await waitFor(() => expect(put).toHaveBeenCalled())
        expect(put.mock.calls[0][1]).toMatchObject({
          id: 1,
        })
        expect(put.mock.calls[0][1]).not.toHaveProperty('expired_time')
      } else {
        fireEvent.click(screen.getAllByRole('button', { name: 'Close' })[0])
      }
      await waitFor(() => expect(close.mock.calls[0]?.[0]).toBe(false))
      view.rerender(tree(first, false))
      await waitFor(() =>
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      )
      view.rerender(tree(second, true))
      await waitFor(() => expect(get).toHaveBeenCalledWith('/api/token/2'))
      expect(findButton('Save changes', true)).toBeDisabled()
      await act(async () =>
        resolveDetail({ data: { success: true, data: second } })
      )
      await waitFor(() =>
        expect(findButton('Save changes', true)).toBeEnabled()
      )
      expect(
        screen.getByRole('button', { name: 'Never expires' })
      ).toBeVisible()
      expect(screen.getByLabelText('Time')).toBeDisabled()
      fireEvent.click(findButton('Save changes', true))
      await waitFor(() =>
        expect(put).toHaveBeenLastCalledWith(
          '/api/token/',
          expect.objectContaining({ id: 2 })
        )
      )
    }
  )

  test.each(['Never', 'Clear'])(
    'submits a non-expiring key when its date is cleared with %s',
    async (action) => {
      const payloads: Array<Record<string, unknown>> = []
      installApiFixtures(payloads)
      await renderCreateDrawer()
      changeInput(getControlByLabel('Name'), 'no-expiry')
      fireEvent.click(screen.getByRole('button', { name: '1 Month' }))
      expect(screen.getByLabelText('Time')).toBeEnabled()
      fireEvent.click(screen.getByRole('button', { name: action }))
      expect(
        screen.getByRole('button', { name: 'Never expires' })
      ).toBeVisible()
      expect(screen.getByLabelText('Time')).toBeDisabled()
      fireEvent.click(findButton('Save changes', true))
      await waitFor(() => expect(payloads).toHaveLength(1))
      expect(payloads[0]?.expired_time).toBe(-1)
    }
  )
})

describe('API keys mutate drawer user quota query switch', () => {
  test('sends false for a new key after the switch, which starts on, is turned off', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    fireEvent.click(findButton('Advanced Settings', true))
    const quotaQuerySwitch = await screen.findByRole('switch', {
      name: 'Allow querying user quota',
    })
    expect(quotaQuerySwitch).toBeChecked()
    fireEvent.click(quotaQuerySwitch)
    expect(quotaQuerySwitch).not.toBeChecked()

    changeInput(getControlByLabel('Name'), 'no-user-quota')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.allow_user_quota_query).toBe(false)
  })
})
