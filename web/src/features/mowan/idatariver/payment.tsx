import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { PaymentConfirmDialog } from '@/features/wallet/components/dialogs/payment-confirm-dialog'

import type { Product } from './api'
import { CnyReference, CnyReferenceNote } from './cny-reference'
import { formatPaymentAmount } from './format'
import type { useIdataRiver } from './use-payment'

type Flow = ReturnType<typeof useIdataRiver>

export function FixedPackages({
  flow,
  onSelect,
}: {
  flow: Flow
  onSelect: (product: Product) => void
}) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  if (
    !flow.loading &&
    !flow.catalogError &&
    !flow.catalog?.enabled &&
    !flow.lookup
  ) {
    return null
  }
  return (
    <section
      className='space-y-3 border-t pt-4'
      aria-label={t('Fixed topup packages')}
    >
      <Label>{t('Fixed topup packages')}</Label>
      {flow.loading && <p role='status'>{t('Loading...')}</p>}
      {flow.catalogError && (
        <Alert>
          <AlertDescription>
            {t('Unable to load payment packages')}
          </AlertDescription>
          <Button variant='outline' onClick={() => void flow.load()}>
            {t('Retry')}
          </Button>
        </Alert>
      )}
      {flow.catalog?.enabled && !flow.catalog.products.length && (
        <p>{t('No payment packages available')}</p>
      )}
      <div className='grid grid-cols-2 gap-2 sm:grid-cols-3'>
        {flow.catalog?.enabled &&
          flow.catalog.products.map((product) => (
            <Button
              key={product.sku_id}
              variant='outline'
              className='h-auto flex-col items-start p-3 text-start whitespace-normal'
              disabled={flow.busy}
              onClick={() => onSelect(product)}
            >
              <span>{product.name}</span>
              <span className='flex flex-wrap items-baseline gap-x-2'>
                <span>{formatPaymentAmount(product.price, language)} USD</span>
                <CnyReference usd={product.price} />
              </span>
              <span className='text-muted-foreground text-xs'>
                {t('Balance credit (USD)')}:{' '}
                {formatPaymentAmount(product.quota, language)}{' '}
                <CnyReference usd={product.quota} />
              </span>
            </Button>
          ))}
      </div>
      <CnyReferenceNote />
      {flow.lookup && (
        <Button variant='outline' onClick={() => flow.setOpen(true)}>
          {t('Resume payment')}
        </Button>
      )}
    </section>
  )
}

export function FixedPaymentDialog({
  flow,
  selected,
  onClear,
}: {
  flow: Flow
  selected: Product | null
  onClear: () => void
}) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  const retrySKU =
    flow.lookup && 'request_id' in flow.lookup
      ? (flow.lookup.sku_id ?? selected?.sku_id)
      : undefined
  const [method, setMethod] = useState('')
  const snapshot = flow.order ?? selected
  const methods = flow.order?.payment_methods ?? []
  const currentMethod = methods.some((item) => item.method === method)
    ? method
    : (methods[0]?.method ?? '')
  const creating = !flow.lookup && !flow.order && !!selected
  let status = flow.lookup ? t('Awaiting payment confirmation') : ''
  if (flow.order?.credited) status = t('Payment confirmed')
  if (flow.order?.platform_status === 'EXPIRED') status = t('Expired')
  if (flow.order?.refund_status && flow.order.refund_status !== 'none') {
    status =
      flow.order.refund_status === 'manual_required'
        ? t('Refund requires manual review')
        : t('Refunded')
  }
  if (flow.order?.can_pay && !methods.length) {
    status = t('No payment methods available')
  }
  const onOpenChange = (open: boolean) => {
    flow.setOpen(open)
    if (!open) onClear()
  }
  if (!creating && (!flow.order?.can_pay || !currentMethod || flow.error)) {
    return (
      <Dialog
        open={flow.open}
        onOpenChange={onOpenChange}
        title={t('Check Status')}
        showCloseButton={false}
        contentClassName='sm:max-w-md'
        footer={
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
        }
      >
        <div aria-live='polite'>
          {flow.busy && <p role='status'>{t('Loading...')}</p>}
          {!flow.busy && flow.error && (
            <ErrorState
              className='min-h-0'
              title={t('Unable to confirm payment. Your order is preserved.')}
              onRetry={() => {
                if (!flow.order && retrySKU) {
                  void flow.create({ sku_id: retrySKU }, true)
                } else if (flow.order) {
                  void flow.query({ trade_no: flow.order.trade_no })
                } else if (flow.lookup) void flow.query(flow.lookup)
              }}
            />
          )}
          {!flow.busy && !flow.error && <p role='status'>{status}</p>}
        </div>
      </Dialog>
    )
  }
  return (
    <PaymentConfirmDialog
      open={flow.open}
      onOpenChange={onOpenChange}
      processing={flow.busy}
      confirmLabel={flow.payment ? t('Continue to payment') : t('Continue')}
      onConfirm={() => {
        if (creating && selected) void flow.create(selected)
        else if (flow.payment) flow.continuePayment(currentMethod)
        else void flow.pay(currentMethod)
      }}
      fixedContent={
        <div className='space-y-4 py-3'>
          {snapshot && (
            <dl className='grid grid-cols-2 gap-3 text-sm'>
              <dt>{t('Package price (USD)')}</dt>
              <dd className='min-w-0 text-end break-all'>
                <span>{formatPaymentAmount(snapshot.price, language)} USD</span>{' '}
                <CnyReference usd={snapshot.price} />
              </dd>
              <dt>{t('Balance credit (USD)')}</dt>
              <dd className='min-w-0 text-end break-all'>
                <span>{formatPaymentAmount(snapshot.quota, language)} USD</span>{' '}
                <CnyReference usd={snapshot.quota} />
              </dd>
              <dt>{t('You Pay')}</dt>
              <dd className='text-end'>
                {flow.payment?.amount != null && flow.payment.pay_currency
                  ? `${formatPaymentAmount(flow.payment.amount, language)} ${flow.payment.pay_currency}`
                  : t('Confirmed on the official payment page')}
              </dd>
            </dl>
          )}
          <CnyReferenceNote />
          <p className='text-muted-foreground text-xs'>
            {t('Payment is completed securely on the official payment page.')}
          </p>
          {flow.order && (
            <p className='text-xs break-all'>{flow.order.trade_no}</p>
          )}
          <p role='status'>{status}</p>
          {flow.order?.can_pay && !flow.error && (
            <fieldset className='space-y-2'>
              <legend className='mb-2 text-sm'>{t('Payment Method')}</legend>
              {methods.map((item) => (
                <label
                  key={item.method}
                  className='flex cursor-pointer items-center gap-2 rounded-md border p-3 text-sm'
                >
                  <input
                    type='radio'
                    name='idatariver-method'
                    value={item.method}
                    checked={item.method === currentMethod}
                    onChange={() => {
                      flow.clearPayment()
                      setMethod(item.method)
                    }}
                    disabled={flow.busy}
                  />
                  {item.name}
                </label>
              ))}
              {!methods.length && <p>{t('No payment methods available')}</p>}
            </fieldset>
          )}
          {flow.lookup && (
            <Button
              variant='outline'
              disabled={flow.busy}
              onClick={() => flow.lookup && void flow.query(flow.lookup)}
            >
              {t('Check Status')}
            </Button>
          )}
        </div>
      }
    />
  )
}
