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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vietnamese from '@/i18n/locales/vi.json'
import zh from '@/i18n/locales/zh.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { ModelCard } from '../components/model-card'
import { ModelDiscountBadge } from '../components/model-discount-badge'
import { PricingTable } from '../components/pricing-table'
import { isPlaygroundChatModel } from '../lib/model-helpers'
import type { PricingModel } from '../types'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async () => {
  const actual = await vi.importActual('@tanstack/react-router')
  return {
    ...actual,
    useRouter: () => ({
      navigate: mockNavigate,
    }),
    useNavigate: () => mockNavigate,
  }
})

function pricingModel(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'test-chat-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: ['default'],
    group_ratio: { default: 1 },
    ...overrides,
  }
}

const resources = { en, fr, ja, ru, vi: vietnamese, zhCN: zh, zhTW }

// Real resources catch interpolation and decimal-separator errors that helper tests miss.
describe('localized discount rendering', () => {
  it.each([
    ['zhCN', 0.000001, '低至 0.00001 折'],
    ['zhTW', 0.000001, '低至 0.00001 折'],
    ['zhCN', 0.99999999999999, '低至 9.9999999999999 折'],
    ['zhCN', 0.9999999999999999, '低至 9.999999999999998 折'],
    ['en', 0.99999999999999, 'Up to 9.99200722163E-13% off'],
    ['fr', 0.995, "Jusqu'à -0,5%"],
    ['ru', 0.995, 'Скидка до 0,5%'],
    ['vi', 0.995, 'Giảm tới 0,5%'],
    ['en', 1e-17, 'Pay from 1E-15%'],
    ['fr', 1e-17, 'À payer dès 1E-15%'],
    ['ru', 1e-17, 'К оплате от 1E-15%'],
    ['ja', 1e-17, '支払いは 1E-15% から'],
    ['vi', 1e-17, 'Thanh toán từ 1E-15%'],
    ['en', 1e-13, 'Up to 99.99999999999% off'],
    ['en', 0.3, 'Up to 70% off'],
    ['fr', 0.3, "Jusqu'à -70%"],
    ['ru', 0.3, 'Скидка до 70%'],
    ['ja', 0.3, '最大 70% OFF'],
    ['vi', 0.3, 'Giảm tới 70%'],
  ])('renders %s ratio %s accurately', async (language, ratio, expected) => {
    const i18n = createInstance()
    await i18n.init({ lng: language as string, fallbackLng: 'en', resources })
    render(
      <I18nextProvider i18n={i18n}>
        <ModelDiscountBadge
          model={pricingModel({ group_ratio: { default: ratio as number } })}
          appearance='card'
        />
      </I18nextProvider>
    )
    expect(screen.getByRole('status')).toHaveTextContent(expected)
  })

  it('updates languages and uses the selected group for exact payment and discount labels', async () => {
    const i18n = createInstance()
    await i18n.init({ lng: 'en', fallbackLng: 'en', resources })
    const model = pricingModel({
      enable_groups: ['default', 'tiny'],
      group_ratio: { default: 0.3, tiny: 1e-17 },
    })
    const { rerender } = render(
      <I18nextProvider i18n={i18n}>
        <ModelDiscountBadge model={model} selectedGroup='tiny' />
      </I18nextProvider>
    )
    expect(screen.getByRole('status')).toHaveTextContent('Pay 1E-15%')
    await act(() => i18n.changeLanguage('fr'))
    expect(screen.getByRole('status')).toHaveTextContent('À payer : 1E-15%')
    await act(() => i18n.changeLanguage('zhCN'))
    expect(screen.getByRole('status')).toHaveTextContent('1E-16 折')
    rerender(
      <I18nextProvider i18n={i18n}>
        <ModelDiscountBadge model={model} selectedGroup='default' />
      </I18nextProvider>
    )
    expect(screen.getByRole('status')).toHaveTextContent('3 折')
    await act(() => i18n.changeLanguage('en'))
    expect(screen.getByRole('status')).toHaveTextContent('70% off')
    await act(() => i18n.changeLanguage('invalid_locale'))
    expect(screen.getByRole('status')).toHaveTextContent('70% off')
  })
})

let queryClient: QueryClient

beforeEach(() => {
  mockNavigate.mockClear()
  useAuthStore.getState().auth.reset('complete')
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
})

afterEach(() => {
  queryClient.clear()
})

describe('model chat entrance & marketplace discount (A1, A2, A3, A5, A6)', () => {
  it.each<Partial<PricingModel>>([
    { model_name: 'doubao-seedance-2-0-260128' },
    { model_name: 'doubao-seedance-1-5-pro' },
    { model_name: 'sora-2' },
    { model_name: 'veo-3.1' },
    { model_name: 'doubao-seedream-4-0' },
    { model_name: 'vendor-image', output_modalities: ['image'] },
    { model_name: 'vendor-video', output_modalities: ['video'] },
    { model_name: 'vendor-vector', capabilities: ['embeddings'] },
    {
      model_name: 'vendor-rerank',
      supported_endpoint_types: ['jina-rerank', 'openai'],
    },
    {
      model_name: 'vendor-video',
      supported_endpoint_types: ['openai-video', 'openai'],
    },
  ])(
    'omits non-chat $model_name from both card and table with generic channel endpoints',
    (overrides) => {
      const model = pricingModel({
        supported_endpoint_types: [
          'openai',
          'openai-response',
          'anthropic',
          'gemini',
        ],
        ...overrides,
      })
      render(
        <QueryClientProvider client={queryClient}>
          <ModelCard model={model} onClick={vi.fn()} />
          <PricingTable models={[model]} onModelClick={vi.fn()} />
        </QueryClientProvider>
      )
      expect(
        screen.queryAllByRole('button', {
          name: /Log in to try|in Playground/i,
        })
      ).toHaveLength(0)
      expect(
        screen.getAllByRole('button', { name: /Copy model name/i })
      ).toHaveLength(2)
      expect(screen.getAllByRole('button', { name: /Details/i })).toHaveLength(
        2
      )
    }
  )

  it('keeps multimodal text conversations available on card and table', () => {
    const model = pricingModel({
      model_name: 'gpt-4o-audio-preview',
      input_modalities: ['text', 'image', 'audio'],
      output_modalities: ['text', 'audio'],
      capabilities: ['vision', 'streaming'],
      supported_endpoint_types: ['openai'],
    })
    render(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} onClick={vi.fn()} />
        <PricingTable models={[model]} onModelClick={vi.fn()} />
      </QueryClientProvider>
    )
    expect(
      screen.getAllByRole('button', {
        name: /Log in to try gpt-4o-audio-preview/i,
      })
    ).toHaveLength(2)
  })

  it('identifies chat models vs non-chat models (A3)', () => {
    // Chat models
    expect(isPlaygroundChatModel(pricingModel({ quota_type: 0 }))).toBe(true)
    expect(
      isPlaygroundChatModel(
        pricingModel({ supported_endpoint_types: ['openai'] })
      )
    ).toBe(true)
    expect(
      isPlaygroundChatModel(
        pricingModel({ supported_endpoint_types: ['anthropic'] })
      )
    ).toBe(true)
    expect(
      isPlaygroundChatModel(
        pricingModel({ supported_endpoint_types: ['gemini'] })
      )
    ).toBe(true)

    // Non-chat models
    // Real backend dall-e-3 combination: ['image-generation', 'openai']
    expect(
      isPlaygroundChatModel(
        pricingModel({
          model_name: 'dall-e-3',
          quota_type: 1,
          supported_endpoint_types: ['image-generation', 'openai'],
        })
      )
    ).toBe(false)
    expect(
      isPlaygroundChatModel(
        pricingModel({
          model_name: 'text-embedding-3-small',
          quota_type: 0,
          supported_endpoint_types: ['embeddings', 'openai'],
        })
      )
    ).toBe(false)
    expect(
      isPlaygroundChatModel(
        pricingModel({
          quota_type: 1,
          supported_endpoint_types: ['image-generation'],
        })
      )
    ).toBe(false)
    expect(
      isPlaygroundChatModel(
        pricingModel({
          quota_type: 1,
          supported_endpoint_types: ['embeddings'],
        })
      )
    ).toBe(false)
    expect(
      isPlaygroundChatModel(
        pricingModel({
          quota_type: 1,
          supported_endpoint_types: ['openai-video'],
        })
      )
    ).toBe(false)
    expect(
      isPlaygroundChatModel(
        pricingModel({
          quota_type: 1,
          supported_endpoint_types: ['jina-rerank'],
        })
      )
    ).toBe(false)
  })

  it('renders discount badge "低至 3 折" on card and navigates to playground when chat icon clicked (A1, A3)', () => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'test-user',
      role: 1,
    })

    const model = pricingModel({
      model_name: 'gpt-4o',
      enable_groups: ['default', 'discount-group'],
      group_ratio: { default: 1, 'discount-group': 0.3 },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} onClick={vi.fn()} />
      </QueryClientProvider>
    )

    // Check discount badge
    expect(screen.getByText(/3 折|3 discount/)).toBeVisible()

    // Check chat entrance button exists
    const chatBtn = screen.getByRole('button', {
      name: /Chat with gpt-4o in Playground|在游乐场体验 gpt-4o/i,
    })
    expect(chatBtn).toBeVisible()

    // Click chat entrance
    fireEvent.click(chatBtn)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/playground',
      search: { model: 'gpt-4o' },
    })

    // Copy button precedes Chat button in visual order (A3)
    const copyBtn = screen.getByRole('button', { name: /Copy model name/i })
    expect(
      Boolean(
        copyBtn.compareDocumentPosition(chatBtn) &
        Node.DOCUMENT_POSITION_FOLLOWING
      )
    ).toBe(true)
    expect(screen.getByRole('button', { name: /Details/i })).toBeVisible()
  })

  it('A1: updates discount dynamically when selectedGroup changes on ModelCard', () => {
    const model = pricingModel({
      model_name: 'dynamic-model',
      enable_groups: ['default', 'vip'],
      group_ratio: { default: 1, vip: 0.5 },
      usable_group: { default: {}, vip: {} },
    })

    const { rerender } = render(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} selectedGroup='' onClick={vi.fn()} />
      </QueryClientProvider>
    )

    // All groups: shows lowest "低至 5 折" / "As low as 5 discount"
    expect(
      screen.getByText(/低至 5 折|As low as 5 discount|Up to 50% off/)
    ).toBeVisible()

    // Select vip group (ratio 0.5): shows exact "5 折" / "5 discount"
    rerender(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} selectedGroup='vip' onClick={vi.fn()} />
      </QueryClientProvider>
    )
    expect(screen.getByText(/5 折|5 discount|50% off/)).toBeVisible()
    expect(
      screen.queryByText(/低至 5 折|As low as 5 discount|Up to 50% off/)
    ).not.toBeInTheDocument()

    // Select default group (ratio 1): ratio >= 1 hides discount
    rerender(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} selectedGroup='default' onClick={vi.fn()} />
      </QueryClientProvider>
    )
    expect(screen.queryByText(/折|discount|off/i)).not.toBeInTheDocument()

    // Switch back to all groups via 'all' sentinel: recovers lowest "低至 5 折"
    rerender(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} selectedGroup='all' onClick={vi.fn()} />
      </QueryClientProvider>
    )
    expect(
      screen.getByText(/低至 5 折|As low as 5 discount|Up to 50% off/)
    ).toBeVisible()
  })

  it('shows "免费" badge when lowest ratio is 0 (A2)', () => {
    const model = pricingModel({
      enable_groups: ['free-tier'],
      group_ratio: { 'free-tier': 0 },
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} onClick={vi.fn()} />
      </QueryClientProvider>
    )

    expect(screen.getByText(/免费|Free/)).toBeVisible()
  })

  it('omits chat button for non-chat models in ModelCard (A3)', () => {
    const imageModel = pricingModel({
      model_name: 'dall-e-3',
      quota_type: 1,
      supported_endpoint_types: ['image-generation'],
    })

    render(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={imageModel} onClick={vi.fn()} />
      </QueryClientProvider>
    )

    expect(
      screen.queryByRole('button', { name: /Playground|在游乐场/i })
    ).not.toBeInTheDocument()
    // Copy and Details are still visible
    expect(
      screen.getByRole('button', { name: /Copy model name/i })
    ).toBeVisible()
    expect(screen.getByRole('button', { name: /Details/i })).toBeVisible()
  })

  it('prompts "登录后体验" when unauthenticated visitor views chat model (A5)', () => {
    useAuthStore.getState().auth.setUser(null)

    const model = pricingModel({ model_name: 'claude-3-haiku' })

    render(
      <QueryClientProvider client={queryClient}>
        <ModelCard model={model} onClick={vi.fn()} />
      </QueryClientProvider>
    )

    const chatBtn = screen.getByRole('button', {
      name: /Log in to try claude-3-haiku|登录后体验 claude-3-haiku/i,
    })
    expect(chatBtn).toBeVisible()

    fireEvent.click(chatBtn)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/playground',
      search: { model: 'claude-3-haiku' },
    })
  })

  it('renders actions column with chat, copy, and details in PricingTable (A3)', () => {
    const chatModel = pricingModel({
      model_name: 'chat-model-1',
      enable_groups: ['default'],
      group_ratio: { default: 0.5 },
    })
    const imageModel = pricingModel({
      id: 2,
      model_name: 'image-model-2',
      quota_type: 1,
      supported_endpoint_types: ['image-generation'],
    })

    const onModelClick = vi.fn()

    render(
      <QueryClientProvider client={queryClient}>
        <PricingTable
          models={[chatModel, imageModel]}
          onModelClick={onModelClick}
        />
      </QueryClientProvider>
    )

    // Chat entrance should be present for chatModel
    const chatBtn = screen.getByRole('button', {
      name: /Chat with chat-model-1 in Playground|Log in to try chat-model-1|在游乐场体验 chat-model-1|登录后体验 chat-model-1/i,
    })
    expect(chatBtn).toBeVisible()

    // Copy button precedes chat button in PricingTable actions column (A3)
    const copyBtns = screen.getAllByRole('button', {
      name: /Copy model name/i,
    })
    expect(
      Boolean(
        copyBtns[0].compareDocumentPosition(chatBtn) &
        Node.DOCUMENT_POSITION_FOLLOWING
      )
    ).toBe(true)

    // Chat entrance should NOT be present for imageModel
    expect(
      screen.queryByRole('button', {
        name: /image-model-2 in Playground|image-model-2/i,
      })
    ).not.toBeInTheDocument()

    // Discount badge shows in table
    expect(screen.getByText(/5 折|5 discount/)).toBeVisible()
  })
})
