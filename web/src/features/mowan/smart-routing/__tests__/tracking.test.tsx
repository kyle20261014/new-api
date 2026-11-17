import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SubmissionTracker } from '../submission-tracker'

test('retrying and refreshing an unknown submission only reads status and shows its updated funding outcome', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', resources: { en: { translation: {} } } })
  const submission = {
    submission_id: 'pending-1',
    task_id: null,
    submission_status: 'unknown',
    reserved_amount: 0.00015,
    reserved_unit: 'USD',
    updated_at: 1000,
    funding_status: 'pending',
    model: 'image',
    group: 'standard',
    submit_time: 900,
  }
  const get = vi
    .spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValueOnce({ data: { success: true, data: submission } })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: { ...submission, funding_status: 'settled', updated_at: 1200 },
      },
    })
  const post = vi.spyOn(api, 'post')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <SubmissionTracker id='pending-1' />
      </I18nextProvider>
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button'))
  expect(await screen.findByText('Funds reserved')).toBeInTheDocument()
  expect(screen.getByText('0.00015 USD')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByText('Settled')).toBeInTheDocument()
  expect(get).toHaveBeenLastCalledWith(
    '/api/mowan/smart-routing/submissions/pending-1'
  )
  expect(post).not.toHaveBeenCalled()
  client.clear()
})
