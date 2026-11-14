import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useSystemOptions } from '@/features/system-settings/hooks/use-system-options'
import { useUpdateOption } from '@/features/system-settings/hooks/use-update-option'

export function IdataRiverSettings() {
  const { t } = useTranslation()
  const { data, isLoading, isError, refetch } = useSystemOptions()
  const update = useUpdateOption()
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [failed, setFailed] = useState(false)
  const options = Object.fromEntries(
    (data?.data ?? []).map(({ key, value }) => [key, value])
  )
  const value = (key: string) => draft[key] ?? options[key] ?? ''
  const change = (key: string, next: string) =>
    setDraft((prev) => ({ ...prev, [key]: next }))
  const fields = [
    ['IdataRiverMerchantId', t('Merchant ID')],
    ['IdataRiverProjectId', t('Project ID')],
    ['IdataRiverCallbackOrigin', t('Callback origin (HTTPS)')],
  ] as const
  const save = async () => {
    setSaving(true)
    setFailed(false)
    try {
      // Keep sales disabled until every configuration field is accepted.
      await update.mutateAsync({ key: 'IdataRiverEnabled', value: false })
      for (const [key, next] of Object.entries(draft)) {
        if (
          key === 'IdataRiverEnabled' ||
          (key === 'IdataRiverApiKey' && !next.trim())
        ) {
          continue
        }
        await update.mutateAsync({ key, value: next })
      }
      await update.mutateAsync({
        key: 'IdataRiverEnabled',
        value: value('IdataRiverEnabled') === 'true',
      })
      setDraft({})
      await refetch()
    } catch {
      setFailed(true)
    } finally {
      setSaving(false)
    }
  }
  if (isLoading) return <p role='status'>{t('Loading...')}</p>
  if (isError) {
    return (
      <Button type='button' onClick={() => void refetch()}>
        {t('Retry')}
      </Button>
    )
  }
  return (
    <section className='space-y-4'>
      <div className='flex items-center gap-3'>
        <Switch
          id='idr-enabled'
          checked={value('IdataRiverEnabled') === 'true'}
          onCheckedChange={(next) => change('IdataRiverEnabled', String(next))}
          disabled={saving}
        />
        <Label htmlFor='idr-enabled'>{t('Enable iDataRiver')}</Label>
      </div>
      {fields.map(([key, label]) => (
        <div key={key} className='space-y-2'>
          <Label htmlFor={key}>{label}</Label>
          <Input
            id={key}
            value={value(key)}
            onChange={(event) => change(key, event.target.value)}
            disabled={saving}
          />
        </div>
      ))}
      <div className='space-y-2'>
        <Label htmlFor='idr-key'>{t('Merchant API key')}</Label>
        <Input
          id='idr-key'
          type='password'
          autoComplete='new-password'
          value={draft.IdataRiverApiKey ?? ''}
          onChange={(event) => change('IdataRiverApiKey', event.target.value)}
          disabled={saving}
        />
        <p className='text-muted-foreground text-xs'>
          {options.IdataRiverKeyConfigured === 'true'
            ? t('Key configured. Leave blank to keep it.')
            : t('No key configured')}
        </p>
      </div>
      <div className='space-y-2'>
        <Label htmlFor='idr-products'>{t('Fixed packages (JSON)')}</Label>
        <Textarea
          id='idr-products'
          rows={8}
          value={value('IdataRiverProducts') || '[]'}
          onChange={(event) => change('IdataRiverProducts', event.target.value)}
          disabled={saving}
        />
        <p className='text-muted-foreground text-xs'>
          {t('Each package requires sku_id, name, price and quota in USD.')}
        </p>
      </div>
      {failed && <p role='alert'>{t('Failed to update setting')}</p>}
      <Button
        type='button'
        disabled={saving || !Object.keys(draft).length}
        onClick={() => void save()}
      >
        {t('Save iDataRiver settings')}
      </Button>
    </section>
  )
}
