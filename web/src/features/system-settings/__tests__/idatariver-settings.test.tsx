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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { describe, expect, it } from 'vitest'

import { Form } from '@/components/ui/form'
import {
  IdataRiverSettingsSection,
  type IdataRiverSettingsValues,
} from '../integrations/idatariver-settings-section'

const DEFAULT_PRODUCTS_JSON = JSON.stringify(
  [
    {
      sku_id: '6adb0f2e7cea3a5b4c41745e',
      name: '支付10U',
      price: 10.123456,
      quota: 10,
    },
    {
      sku_id: '6adb0f522306d6eb100fe324',
      name: '支付20U',
      price: 20,
      quota: 20,
    },
    {
      sku_id: '6adb0f622306d6eb100fe325',
      name: '支付50U',
      price: 50,
      quota: 50,
    },
    {
      sku_id: '6adb0f732306d6eb100fe326',
      name: '支付100U',
      price: 100,
      quota: 100,
    },
  ],
  null,
  2
)

function TestSettingsWrapper({
  initialVisualMode = true,
}: {
  initialVisualMode?: boolean
}) {
  const [visualMode, setVisualMode] = useState(initialVisualMode)
  const form = useForm<IdataRiverSettingsValues>({
    defaultValues: {
      IdataRiverEnabled: true,
      IdataRiverApiKey: '',
      IdataRiverMerchantId: '6adaf86e4a9d53fa7ffbfa10',
      IdataRiverProjectId: '6adb0ecdc5c1b963f6862493',
      IdataRiverProducts: DEFAULT_PRODUCTS_JSON,
      IdataRiverCallbackOrigin: 'https://mw.vistalineglobal.com',
    },
  })

  return (
    <Form {...form}>
      <form>
        <IdataRiverSettingsSection
          form={form}
          productsVisualMode={visualMode}
          onToggleProductsVisualMode={() => setVisualMode(!visualMode)}
        />
      </form>
    </Form>
  )
}

describe('IdataRiverSettingsSection', () => {
  it('renders default product table in visual mode without crashing (StaticDataTable regression test)', () => {
    render(<TestSettingsWrapper initialVisualMode />)

    // Verify section header and fields
    expect(screen.getByText('iDataRiver Gateway')).toBeInTheDocument()
    expect(
      screen.getByDisplayValue('6adaf86e4a9d53fa7ffbfa10')
    ).toBeInTheDocument()
    expect(
      screen.getByDisplayValue('6adb0ecdc5c1b963f6862493')
    ).toBeInTheDocument()
    expect(
      screen.getByDisplayValue('https://mw.vistalineglobal.com')
    ).toBeInTheDocument()

    // Verify product package table headers and items
    expect(screen.getByText('支付10U')).toBeInTheDocument()
    expect(screen.getByText('6adb0f2e7cea3a5b4c41745e')).toBeInTheDocument()
    expect(screen.getByText('支付20U')).toBeInTheDocument()
    expect(screen.getByText('支付50U')).toBeInTheDocument()
    expect(screen.getByText('支付100U')).toBeInTheDocument()

    // USD columns preserve the configured quote without display-currency conversion.
    expect(screen.queryByText('$10')).not.toBeInTheDocument()
    expect(screen.getByText('10.123456')).toBeInTheDocument()
    expect(screen.getByText('10')).toBeInTheDocument()
  })

  it('can toggle between visual editor and JSON code editor', async () => {
    const user = userEvent.setup()
    render(<TestSettingsWrapper initialVisualMode />)

    // Click JSON Editor button
    const jsonBtn = screen.getByRole('button', { name: /JSON Editor/i })
    await user.click(jsonBtn)

    // Code editor should now be visible
    expect(
      screen.getByRole('button', { name: /Visual Editor/i })
    ).toBeInTheDocument()
  })

  it('opens add product dialog and adds a new package', async () => {
    const user = userEvent.setup()
    render(<TestSettingsWrapper initialVisualMode />)

    const addBtn = screen.getByRole('button', { name: /Add Product/i })
    await user.click(addBtn)

    // Dialog should open
    expect(screen.getByText('Add Product Package')).toBeInTheDocument()

    // Fill in package details
    const skuInput = screen.getByPlaceholderText('6adb0f2e7cea3a5b4c41745e')
    await user.clear(skuInput)
    await user.type(skuInput, '6adb0f999999999999999999')

    const nameInput = screen.getByPlaceholderText('10 USD')
    await user.clear(nameInput)
    await user.type(nameInput, '支付200U')

    const saveBtn = screen.getByRole('button', { name: 'Save' })
    await user.click(saveBtn)

    // Dialog should close and new package should be in the table
    await waitFor(() => {
      expect(screen.queryByText('Add Product Package')).not.toBeInTheDocument()
    })
    expect(screen.getByText('支付200U')).toBeInTheDocument()
  })
})
