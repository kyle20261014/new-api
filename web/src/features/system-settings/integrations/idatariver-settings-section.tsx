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
import { zodResolver } from '@hookform/resolvers/zod'
import { Code2, Eye, Plus } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { Dialog } from '@/components/dialog'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatLocalCurrencyAmount } from '@/lib/currency'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { safeJsonParseWithValidation } from '../utils/json-parser'
import { isArray } from '../utils/json-validators'
import { safeNumberFieldProps } from '../utils/numeric-field'

export type IdataRiverSettingsValues = {
  IdataRiverEnabled: boolean
  IdataRiverApiKey: string
  IdataRiverMerchantId: string
  IdataRiverProjectId: string
  IdataRiverProducts: string
  IdataRiverCallbackOrigin: string
}

export interface IdataRiverProductItem {
  sku_id: string
  name: string
  price: number
  quota: number
}

const idatariverProductDialogSchema = z.object({
  sku_id: z
    .string()
    .trim()
    .regex(/^[a-f0-9]{24}$/, 'SKU ID must be a 24-character hexadecimal ID'),
  name: z.string().trim().min(1, 'Product name is required').max(200),
  price: z.number().min(0.01, 'Price must be greater than 0'),
  quota: z.number().min(0.01, 'Quota must be greater than 0'),
})

type IdataRiverProductDialogValues = z.infer<
  typeof idatariverProductDialogSchema
>

interface ProductDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSave: (product: IdataRiverProductItem) => void
  editData: IdataRiverProductItem | null
}

function IdataRiverProductDialog({
  open,
  onOpenChange,
  onSave,
  editData,
}: ProductDialogProps) {
  const { t } = useTranslation()
  const form = useForm<IdataRiverProductDialogValues>({
    resolver: zodResolver(idatariverProductDialogSchema),
    defaultValues: {
      sku_id: '',
      name: '',
      price: 10,
      quota: 10,
    },
  })

  useEffect(() => {
    if (editData) {
      form.reset(editData)
    } else {
      form.reset({
        sku_id: '',
        name: '',
        price: 10,
        quota: 10,
      })
    }
  }, [editData, form])

  const onSubmit = (values: IdataRiverProductDialogValues) => {
    onSave(values)
    onOpenChange(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={editData ? t('Edit Product Package') : t('Add Product Package')}
      description={t('Configure iDataRiver recharge SKU, price, and quota')}
      contentClassName="sm:max-w-md"
    >
      <Form {...form}>
        <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
          <FormField
            control={form.control}
            name="sku_id"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('SKU ID')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder="6adb0f2e7cea3a5b4c41745e"
                    {...field}
                    disabled={Boolean(editData)}
                  />
                </FormControl>
                <FormDescription>
                  {t('24-character iDataRiver product SKU ID')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="name"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Product Name')}</FormLabel>
                <FormControl>
                  <Input placeholder="10 USD" {...field} />
                </FormControl>
                <FormDescription>
                  {t('Display name shown to users')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div className="grid grid-cols-2 gap-4">
            <FormField
              control={form.control}
              name="price"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Price (USD)')}</FormLabel>
                  <FormControl>
                    <Input
                      type="number"
                      step="0.01"
                      min={0.01}
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Payment price charged in USD')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name="quota"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Quota (USD)')}</FormLabel>
                  <FormControl>
                    <Input
                      type="number"
                      step="0.01"
                      min={0.01}
                      {...safeNumberFieldProps(field)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Wallet balance credited in USD')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button type="submit">{t('Save')}</Button>
          </div>
        </form>
      </Form>
    </Dialog>
  )
}

interface IdataRiverProductsVisualEditorProps {
  value: string
  onChange: (value: string) => void
}

function IdataRiverProductsVisualEditor({
  value,
  onChange,
}: IdataRiverProductsVisualEditorProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editProduct, setEditProduct] = useState<IdataRiverProductItem | null>(
    null
  )

  const products = useMemo(() => {
    const parsed = safeJsonParseWithValidation<unknown[]>(value, {
      fallback: [],
      validator: isArray,
      validatorMessage: t('iDataRiver products must be a JSON array'),
      context: 'idatariver products',
    })

    return parsed.filter(
      (item): item is IdataRiverProductItem =>
        typeof item === 'object' &&
        item !== null &&
        'sku_id' in item &&
        'name' in item &&
        'price' in item &&
        'quota' in item &&
        typeof item.sku_id === 'string' &&
        typeof item.name === 'string' &&
        typeof item.price === 'number' &&
        typeof item.quota === 'number'
    )
  }, [value, t])

  const handleSaveProduct = (product: IdataRiverProductItem) => {
    const existingIndex = products.findIndex((p) => p.sku_id === product.sku_id)
    let updated: IdataRiverProductItem[]
    if (existingIndex >= 0) {
      updated = products.map((p, idx) => (idx === existingIndex ? product : p))
    } else {
      updated = [...products, product]
    }
    onChange(JSON.stringify(updated, null, 2))
    toast.success(t('Product package updated'))
  }

  const handleDeleteProduct = (sku_id: string) => {
    const updated = products.filter((p) => p.sku_id !== sku_id)
    onChange(JSON.stringify(updated, null, 2))
    toast.success(t('Product package removed'))
  }

  const columns: StaticDataTableColumn<IdataRiverProductItem>[] = useMemo(
    () => [
      {
        id: 'sku_id',
        header: t('SKU ID'),
        cell: (item: IdataRiverProductItem) => (
          <code className="bg-muted rounded px-1.5 py-0.5 font-mono text-xs">
            {item.sku_id}
          </code>
        ),
      },
      {
        id: 'name',
        header: t('Name'),
        cell: (item: IdataRiverProductItem) => <span>{item.name}</span>,
      },
      {
        id: 'price',
        header: t('Price (USD)'),
        cell: (item: IdataRiverProductItem) => (
          <span>
            {formatLocalCurrencyAmount(item.price, {
              showSymbol: false,
              abbreviate: false,
              digitsLarge: 20,
              digitsSmall: 20,
              locale,
            })}
          </span>
        ),
      },
      {
        id: 'quota',
        header: t('Quota (USD)'),
        cell: (item: IdataRiverProductItem) => (
          <span>
            {formatLocalCurrencyAmount(item.quota, {
              showSymbol: false,
              abbreviate: false,
              digitsLarge: 20,
              digitsSmall: 20,
              locale,
            })}
          </span>
        ),
      },
      {
        id: 'actions',
        header: t('Actions'),
        cell: (item: IdataRiverProductItem) => (
          <StaticRowActions
            editLabel={t('Edit product')}
            deleteLabel={t('Delete product')}
            menuLabel={t('Product actions')}
            onEdit={() => {
              setEditProduct(item)
              setDialogOpen(true)
            }}
            onDelete={() => handleDeleteProduct(item.sku_id)}
          />
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [t, products, locale]
  )

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <Label className="text-muted-foreground text-xs font-medium uppercase">
          {t('Configured Packages')} ({products.length})
        </Label>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => {
            setEditProduct(null)
            setDialogOpen(true)
          }}
          className="gap-1.5"
        >
          <Plus className="h-3.5 w-3.5" />
          {t('Add Product')}
        </Button>
      </div>

      <div className="rounded-md border">
        <StaticDataTable<IdataRiverProductItem>
          columns={columns}
          data={products}
          getRowKey={(product) => product.sku_id}
          emptyContent={t('No products configured yet')}
        />
      </div>

      <IdataRiverProductDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onSave={handleSaveProduct}
        editData={editProduct}
      />
    </div>
  )
}

export interface IdataRiverSettingsSectionProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  form: UseFormReturn<any>
  productsVisualMode: boolean
  onToggleProductsVisualMode: () => void
}

export function IdataRiverSettingsSection({
  form,
  productsVisualMode,
  onToggleProductsVisualMode,
}: IdataRiverSettingsSectionProps) {
  const { t } = useTranslation()
  const callbackOrigin =
    form.watch('IdataRiverCallbackOrigin') || 'https://mw.vistalineglobal.com'

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-lg font-medium">{t('iDataRiver Gateway')}</h3>
        <p className="text-muted-foreground text-sm">
          {t('Configuration for iDataRiver merchant payment integration')}
        </p>
      </div>

      <div className="rounded-md bg-blue-50 p-4 text-sm text-blue-900 dark:bg-blue-950 dark:text-blue-100">
        <p className="mb-2 font-medium">{t('Callback Configuration:')}</p>
        <ul className="list-inside list-disc space-y-1 text-xs sm:text-sm">
          <li>
            {t('Callback URL:')}{' '}
            <code className="rounded bg-blue-100 px-1 py-0.5 font-mono text-xs dark:bg-blue-900">
              {`${callbackOrigin}/api/user/idatariver/notify`}
            </code>
          </li>
          <li>
            {t('Return URL:')}{' '}
            <code className="rounded bg-blue-100 px-1 py-0.5 font-mono text-xs dark:bg-blue-900">
              {`${callbackOrigin}/wallet`}
            </code>
          </li>
          <li>{t('Configure in your iDataRiver merchant console')}</li>
        </ul>
      </div>

      <FormField
        control={form.control}
        name="IdataRiverEnabled"
        render={({ field }) => (
          <SettingsSwitchItem>
            <SettingsSwitchContent>
              <FormLabel>{t('Enable iDataRiver')}</FormLabel>
              <FormDescription>
                {t('Enable wallet top-up via iDataRiver merchant gateway')}
              </FormDescription>
            </SettingsSwitchContent>
            <FormControl>
              <Switch checked={field.value} onCheckedChange={field.onChange} />
            </FormControl>
          </SettingsSwitchItem>
        )}
      />

      <div className="grid gap-6 md:grid-cols-2">
        <FormField
          control={form.control}
          name="IdataRiverApiKey"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('API Key')}</FormLabel>
              <FormControl>
                <Input
                  type="password"
                  placeholder={t(
                    'Developer API key (leave blank to preserve existing key)'
                  )}
                  autoComplete="new-password"
                  {...field}
                />
              </FormControl>
              <FormDescription>
                {t('Developer API key (leave blank to preserve existing key)')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name="IdataRiverCallbackOrigin"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Callback Origin')}</FormLabel>
              <FormControl>
                <Input
                  placeholder="https://mw.vistalineglobal.com"
                  {...field}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'Trusted HTTPS origin without path (e.g. https://mw.vistalineglobal.com)'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <FormField
          control={form.control}
          name="IdataRiverMerchantId"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Merchant ID')}</FormLabel>
              <FormControl>
                <Input placeholder="6adaf86e4a9d53fa7ffbfa10" {...field} />
              </FormControl>
              <FormDescription>
                {t('24-character iDataRiver merchant ID')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name="IdataRiverProjectId"
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Project ID')}</FormLabel>
              <FormControl>
                <Input placeholder="6adb0ecdc5c1b963f6862493" {...field} />
              </FormControl>
              <FormDescription>
                {t('24-character iDataRiver project ID')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>

      <FormField
        control={form.control}
        name="IdataRiverProducts"
        render={({ field }) => (
          <FormItem>
            <div className="mb-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <FormLabel>{t('Recharge Packages (Products)')}</FormLabel>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={onToggleProductsVisualMode}
                className="w-full sm:w-auto"
              >
                {productsVisualMode ? (
                  <>
                    <Code2 className="mr-2 h-3 w-3" />
                    {t('JSON Editor')}
                  </>
                ) : (
                  <>
                    <Eye className="mr-2 h-3 w-3" />
                    {t('Visual Editor')}
                  </>
                )}
              </Button>
            </div>
            <FormControl>
              {productsVisualMode ? (
                <IdataRiverProductsVisualEditor
                  value={field.value}
                  onChange={field.onChange}
                />
              ) : (
                <JsonCodeEditor
                  value={field.value}
                  onChange={field.onChange}
                  name={field.name}
                  onBlur={field.onBlur}
                  textareaRef={field.ref}
                  placeholder='[{"sku_id":"6adb0f2e7cea3a5b4c41745e","name":"支付10U","price":10,"quota":10}]'
                  heightClassName="h-48 min-h-48 max-h-48"
                  aria-invalid={Boolean(
                    form.formState.errors.IdataRiverProducts
                  )}
                />
              )}
            </FormControl>
            <FormDescription>
              {t(
                'Configure iDataRiver fixed recharge packages with SKU ID, display name, price USD, and quota USD.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </div>
  )
}
