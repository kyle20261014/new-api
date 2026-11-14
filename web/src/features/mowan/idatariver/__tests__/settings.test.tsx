import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'

import { IdataRiverSettings } from '../settings'

const { update, refetch } = vi.hoisted(() => ({
  update: vi.fn(),
  refetch: vi.fn(),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/features/system-settings/hooks/use-update-option', () => ({
  useUpdateOption: () => ({ mutateAsync: update }),
}))
vi.mock('@/features/system-settings/hooks/use-system-options', () => ({
  useSystemOptions: () => ({
    data: {
      data: [
        { key: 'IdataRiverEnabled', value: 'true' },
        { key: 'IdataRiverKeyConfigured', value: 'true' },
        { key: 'IdataRiverMerchantId', value: 'merchant' },
      ],
    },
    isLoading: false,
    isError: false,
    refetch,
  }),
}))

beforeEach(() => {
  vi.clearAllMocks()
  update.mockResolvedValue({ success: true })
})

it('preserves the configured secret and enables sales only after accepted configuration', async () => {
  render(<IdataRiverSettings />)
  expect(screen.getByLabelText('Merchant API key')).toHaveValue('')
  fireEvent.change(screen.getByLabelText('Merchant ID'), {
    target: { value: 'new-merchant' },
  })
  fireEvent.click(
    screen.getByRole('button', { name: 'Save iDataRiver settings' })
  )
  await waitFor(() => expect(refetch).toHaveBeenCalledTimes(1))
  expect(update.mock.calls.map(([request]) => request)).toEqual([
    { key: 'IdataRiverEnabled', value: false },
    { key: 'IdataRiverMerchantId', value: 'new-merchant' },
    { key: 'IdataRiverEnabled', value: true },
  ])
})

it('does not enable payments after invalid package configuration', async () => {
  update
    .mockResolvedValueOnce({ success: true })
    .mockRejectedValueOnce(new Error('invalid packages'))
  render(<IdataRiverSettings />)
  fireEvent.change(screen.getByLabelText('Fixed packages (JSON)'), {
    target: { value: 'invalid' },
  })
  fireEvent.click(
    screen.getByRole('button', { name: 'Save iDataRiver settings' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Failed to update setting'
  )
  expect(update.mock.calls.map(([request]) => request)).toEqual([
    { key: 'IdataRiverEnabled', value: false },
    { key: 'IdataRiverProducts', value: 'invalid' },
  ])
  expect(refetch).not.toHaveBeenCalled()
})
