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
  ExternalLink,
  Loader2,
  RefreshCw,
  Search,
  ShieldAlert,
  WalletCards,
} from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import {
  formatBillingCurrencyFromUSD,
  formatCurrencyFromUSD,
  formatLocalCurrencyAmount,
} from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import {
  checkIdatarRiverOrder,
  createIdatarRiverOrder,
  getIdatarRiverProducts,
  payIdatarRiverOrder,
} from '../api'
import { evaluateTopupOutcome } from '../lib/billing'
import { isAlipayOrWeChat, isSafePaymentUrl } from '../lib/idatariver'
import type {
  IdataRiverOrderResponseData,
  IdataRiverPayResponseData,
  IdataRiverProduct,
} from '../types'

export interface IdataRiverTopupProps {
  onSuccess?: () => void
  onOpenBilling?: () => void
  disabled?: boolean
}

const SKELETON_ITEMS = ['sk-1', 'sk-2', 'sk-3', 'sk-4', 'sk-5']

export function IdataRiverTopup(props: IdataRiverTopupProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const currencyOptions = {
    locale,
    digitsLarge: 20,
    digitsSmall: 20,
    abbreviate: false,
  }
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [products, setProducts] = useState<IdataRiverProduct[]>([])

  // Purchase & Order State
  const [orderingSku, setOrderingSku] = useState<string | null>(null)
  const [currentOrder, setCurrentOrder] =
    useState<IdataRiverOrderResponseData | null>(null)
  const [selectedMethod, setSelectedMethod] = useState<string>('')
  const [paymentDialogOpen, setPaymentDialogOpen] = useState(false)
  const [payData, setPayData] = useState<IdataRiverPayResponseData | null>(null)
  const [paying, setPaying] = useState(false)
  const [paidAttempted, setPaidAttempted] = useState(false)
  const [reconciling, setReconciling] = useState(false)

  // Manual Reconcile Dialog State
  const [manualCheckOpen, setManualCheckOpen] = useState(false)
  const [manualTradeNo, setManualTradeNo] = useState('')
  const [manualChecking, setManualChecking] = useState(false)

  const fetchProducts = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const res = await getIdatarRiverProducts()
      if (res.success && res.data) {
        setEnabled(Boolean(res.data.enabled))
        setProducts(Array.isArray(res.data.products) ? res.data.products : [])
      } else {
        setEnabled(false)
        setProducts([])
        setError(res.message || t('Failed to load iDataRiver products'))
      }
    } catch (err) {
      setEnabled(false)
      setProducts([])
      setError(
        err instanceof Error
          ? err.message
          : t('Failed to load iDataRiver products')
      )
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void fetchProducts()
  }, [fetchProducts])

  // Start ordering a product
  const handleSelectProduct = async (product: IdataRiverProduct) => {
    if (props.disabled || orderingSku) return

    setOrderingSku(product.sku_id)
    try {
      const res = await createIdatarRiverOrder(product.sku_id)
      if (res.success && res.data) {
        setCurrentOrder(res.data)
        const defaultMethod = res.data.payment_methods?.[0]?.method || ''
        setSelectedMethod(defaultMethod)
        setPayData(null)
        setPaidAttempted(false)
        setPaymentDialogOpen(true)
      } else {
        handleServerError(res, t('Failed to create top-up order'))
      }
    } catch (err) {
      handleServerError(err, t('Failed to create top-up order'))
    } finally {
      setOrderingSku(null)
    }
  }

  // Pay the order
  const handlePay = async () => {
    if (!currentOrder || !selectedMethod || paying) return

    setPaying(true)
    try {
      const res = await payIdatarRiverOrder(
        currentOrder.trade_no,
        selectedMethod
      )
      if (res.success && res.data?.pay_url) {
        const url = res.data.pay_url
        if (!isSafePaymentUrl(url)) {
          toast.error(t('Invalid iDataRiver payment link'))
          return
        }

        setPayData(res.data)
        setPaidAttempted(true)
        window.open(url, '_blank', 'noopener,noreferrer')
        toast.info(
          t('Payment window opened. Please complete payment and check status.')
        )
      } else {
        handleServerError(res, t('Failed to get payment link'))
      }
    } catch (err) {
      handleServerError(err, t('Failed to get payment link'))
    } finally {
      setPaying(false)
    }
  }

  // Reconcile order status
  const handleReconcile = async () => {
    if (!currentOrder || reconciling) return

    setReconciling(true)
    try {
      const res = await checkIdatarRiverOrder(currentOrder.trade_no)
      if (res.success && res.data) {
        const updated = res.data
        setCurrentOrder(updated)

        const outcome = evaluateTopupOutcome(
          updated.status,
          updated.refund_status
        )
        if (outcome.kind === 'success') {
          toast.success(t(outcome.messageKey))
          props.onSuccess?.()
          setPaymentDialogOpen(false)
        } else if (
          outcome.kind === 'refunded_recovered' ||
          outcome.kind === 'refunded_before_credit'
        ) {
          toast.info(t(outcome.messageKey))
          props.onSuccess?.()
          setPaymentDialogOpen(false)
        } else if (outcome.kind === 'refunded_manual') {
          toast.warning(t(outcome.messageKey))
        } else if (outcome.kind === 'expired') {
          toast.warning(t(outcome.messageKey))
        } else if (outcome.kind === 'pending') {
          toast.info(t(outcome.messageKey))
        } else {
          toast.info(t(outcome.messageKey, { status: updated.status }))
        }
      } else {
        handleServerError(res, t('Failed to check order status'))
      }
    } catch (err) {
      handleServerError(err, t('Failed to check order status'))
    } finally {
      setReconciling(false)
    }
  }

  // Manual Check Handler
  const handleManualCheck = async () => {
    const trimmed = manualTradeNo.trim()
    if (!trimmed || manualChecking) return

    setManualChecking(true)
    try {
      const res = await checkIdatarRiverOrder(trimmed)
      if (res.success && res.data) {
        const order = res.data
        const outcome = evaluateTopupOutcome(order.status, order.refund_status)
        if (outcome.kind === 'success') {
          toast.success(t(outcome.messageKey))
          props.onSuccess?.()
          setManualCheckOpen(false)
          setManualTradeNo('')
        } else if (
          outcome.kind === 'refunded_recovered' ||
          outcome.kind === 'refunded_before_credit'
        ) {
          toast.info(t(outcome.messageKey))
          props.onSuccess?.()
          setManualCheckOpen(false)
          setManualTradeNo('')
        } else if (outcome.kind === 'refunded_manual') {
          toast.warning(t(outcome.messageKey))
        } else if (outcome.kind === 'expired') {
          toast.warning(t(outcome.messageKey))
        } else if (outcome.kind === 'pending') {
          toast.info(t(outcome.messageKey))
        } else {
          toast.info(t(outcome.messageKey, { status: order.status }))
        }
      } else {
        handleServerError(res, t('Failed to check order status'))
      }
    } catch (err) {
      handleServerError(err, t('Failed to check order status'))
    } finally {
      setManualChecking(false)
    }
  }

  const renderPackageContent = () => {
    if (error) {
      return (
        <Alert variant="destructive">
          <ShieldAlert className="h-4 w-4" />
          <AlertTitle>{t('Failed to load packages')}</AlertTitle>
          <AlertDescription className="flex items-center justify-between gap-2">
            <span>{error}</span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void fetchProducts()}
            >
              <RefreshCw className="mr-1.5 h-3 w-3" />
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )
    }

    if (!enabled) {
      return (
        <Alert>
          <AlertDescription>
            {t('iDataRiver top-up is currently unavailable.')}
          </AlertDescription>
        </Alert>
      )
    }

    if (products.length === 0) {
      return (
        <EmptyState
          title={t('No packages available')}
          description={t(
            'No fixed packages configured. Please check back later.'
          )}
        />
      )
    }

    return (
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 sm:gap-3 lg:grid-cols-5">
        {products.map((product) => {
          const isOrdering = orderingSku === product.sku_id
          return (
            <Button
              key={product.sku_id}
              variant="outline"
              disabled={props.disabled || Boolean(orderingSku)}
              onClick={() => handleSelectProduct(product)}
              className={cn(
                'flex h-auto min-h-28 flex-col items-start justify-between rounded-lg p-3 text-left sm:p-4',
                'hover:border-primary/50 focus-visible:ring-ring focus-visible:ring-2'
              )}
              aria-label={`${product.name}, ${t('Quota')}: ${formatCurrencyFromUSD(product.quota, { locale })}, ${t('Price')}: ${formatBillingCurrencyFromUSD(product.price, { locale })}`}
            >
              <div className="w-full">
                <div className="text-sm font-semibold sm:text-base">
                  {product.name}
                </div>
                <div className="text-muted-foreground mt-1 text-xs">
                  {t('Quota')}:{' '}
                  {formatCurrencyFromUSD(product.quota, { locale })}
                </div>
              </div>
              <div className="mt-3 flex w-full items-center justify-between border-t border-border/40 pt-2">
                <span className="text-primary text-base font-bold sm:text-lg">
                  {formatBillingCurrencyFromUSD(product.price, { locale })}
                </span>
                {isOrdering && (
                  <Loader2 className="text-muted-foreground h-4 w-4 animate-spin" />
                )}
              </div>
            </Button>
          )
        })}
      </div>
    )
  }

  // If loading, show skeleton
  if (loading) {
    return (
      <Card data-card-hover="false" className="gap-0 overflow-hidden py-0">
        <div className="border-b p-3 sm:p-5">
          <Skeleton className="h-6 w-40" />
          <Skeleton className="mt-2 h-4 w-60" />
        </div>
        <CardContent className="p-3 sm:p-5">
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 sm:gap-3 lg:grid-cols-5">
            {SKELETON_ITEMS.map((key) => (
              <Skeleton key={key} className="h-28 rounded-lg" />
            ))}
          </div>
        </CardContent>
      </Card>
    )
  }

  return (
    <>
      <TitledCard
        title={t('Fixed Top-up Packages')}
        description={t('Select a package to recharge your wallet balance')}
        icon={<WalletCards className="h-4 w-4" />}
        iconTone="primary"
        disableHoverEffect
        action={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setManualCheckOpen(true)}
              className="gap-1.5"
              aria-label={t('Check Order Status')}
            >
              <Search className="h-3.5 w-3.5" />
              {t('Check Order Status')}
            </Button>
          </div>
        }
        contentClassName="space-y-4"
      >
        {renderPackageContent()}
      </TitledCard>

      {/* Payment Confirmation & Method Selection Dialog */}
      <Dialog
        open={paymentDialogOpen}
        onOpenChange={(open) => {
          if (!paying && !reconciling) {
            setPaymentDialogOpen(open)
          }
        }}
        title={t('iDataRiver Payment')}
        description={t('Choose a payment method to complete your top-up')}
        contentClassName="sm:max-w-md"
      >
        {currentOrder && (
          <div className="space-y-4">
            {/* Order Details */}
            <div className="bg-muted/40 space-y-2 rounded-lg border p-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">
                  {t('Order Number')}
                </span>
                <div className="flex items-center gap-1 font-mono text-xs">
                  <span className="max-w-[180px] truncate">
                    {currentOrder.trade_no}
                  </span>
                  <CopyButton value={currentOrder.trade_no} />
                </div>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">
                  {t('Recharge Quota')}
                </span>
                <span className="text-foreground font-semibold">
                  {formatCurrencyFromUSD(currentOrder.quota, { locale })}
                </span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">
                  {t('Wallet Price')}
                </span>
                <span className="text-muted-foreground font-mono">
                  {formatLocalCurrencyAmount(currentOrder.price, {
                    ...currencyOptions,
                    showSymbol: false,
                  })}{' '}
                  {currentOrder.currency}
                </span>
              </div>

              {/* Visibly distinct platform payable amount/currency (A3 requirement) */}
              {payData && (
                <div className="flex items-center justify-between border-t border-border/40 pt-2">
                  <span className="text-foreground font-medium">
                    {t('Platform Payable Amount')}
                  </span>
                  <span className="text-primary font-mono text-base font-bold">
                    {formatLocalCurrencyAmount(
                      typeof payData.amount === 'number'
                        ? payData.amount
                        : Number.parseFloat(String(payData.amount)),
                      {
                        ...currencyOptions,
                        showSymbol: false,
                      }
                    )}{' '}
                    {payData.pay_currency}
                  </span>
                </div>
              )}

              <div className="flex items-center justify-between border-t border-border/40 pt-2">
                <span className="text-muted-foreground">{t('Status')}</span>
                <StatusBadge
                  label={t(currentOrder.status)}
                  variant={
                    currentOrder.status === 'success' ? 'success' : 'warning'
                  }
                  showDot
                  copyable={false}
                />
              </div>
            </div>

            {/* Waiting for payment guidance notice */}
            {paidAttempted && (
              <Alert className="border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-400">
                <AlertDescription className="text-xs">
                  {t(
                    'Please complete the payment on the payment platform, then click Check Payment Status.'
                  )}
                </AlertDescription>
              </Alert>
            )}

            {/* Payment Methods */}
            {currentOrder.status === 'pending' && !paidAttempted && (
              <div className="space-y-3">
                <Label className="text-muted-foreground text-xs font-medium tracking-wider uppercase">
                  {t('Payment Method')}
                </Label>
                {currentOrder.payment_methods.length === 0 ? (
                  <Alert>
                    <AlertDescription>
                      {t('No payment methods available for this order.')}
                    </AlertDescription>
                  </Alert>
                ) : (
                  <div className="grid grid-cols-2 gap-2">
                    {currentOrder.payment_methods.map((method) => {
                      const isSelected = selectedMethod === method.method
                      return (
                        <Button
                          key={method.method}
                          type="button"
                          variant={isSelected ? 'default' : 'outline'}
                          aria-pressed={isSelected}
                          className={cn(
                            'justify-center text-sm font-medium',
                            isSelected && 'ring-primary ring-2'
                          )}
                          onClick={() => setSelectedMethod(method.method)}
                        >
                          {method.name}
                        </Button>
                      )
                    })}
                  </div>
                )}

                {/* Required Network Notice for Alipay and WeChat Pay */}
                {isAlipayOrWeChat(
                  selectedMethod,
                  currentOrder.payment_methods.find(
                    (m) => m.method === selectedMethod
                  )?.name
                ) && (
                  <Alert className="border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400">
                    <ShieldAlert className="h-4 w-4" />
                    <AlertDescription className="text-xs">
                      {t(
                        'If you cannot open the payment page, please turn off VPN or switch networks.'
                      )}
                    </AlertDescription>
                  </Alert>
                )}
              </div>
            )}

            {/* Re-open payment link button if payment window was opened */}
            {paidAttempted && payData?.pay_url && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  if (isSafePaymentUrl(payData.pay_url)) {
                    window.open(
                      payData.pay_url,
                      '_blank',
                      'noopener,noreferrer'
                    )
                  }
                }}
                className="w-full gap-2"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                {t('Open Payment Page Again')}
              </Button>
            )}

            {/* Action Buttons */}
            <div className="flex flex-col gap-2 pt-2 sm:flex-row sm:justify-end">
              <Button
                variant="outline"
                onClick={() => setPaymentDialogOpen(false)}
                disabled={paying || reconciling}
              >
                {paidAttempted ? t('Close') : t('Cancel')}
              </Button>
              {paidAttempted ? (
                <Button
                  onClick={handleReconcile}
                  disabled={reconciling}
                  className="gap-2"
                >
                  {reconciling ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <RefreshCw className="h-4 w-4" />
                  )}
                  {t('Check Payment Status')}
                </Button>
              ) : (
                <Button
                  onClick={handlePay}
                  disabled={
                    paying ||
                    !selectedMethod ||
                    currentOrder.payment_methods.length === 0
                  }
                  className="gap-2"
                >
                  {paying ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <ExternalLink className="h-4 w-4" />
                  )}
                  {t('Proceed to Pay')}
                </Button>
              )}
            </div>
          </div>
        )}
      </Dialog>

      {/* Manual Check Order Dialog */}
      <Dialog
        open={manualCheckOpen}
        onOpenChange={(open) => {
          if (!manualChecking) {
            setManualCheckOpen(open)
          }
        }}
        title={t('Check Order Status')}
        description={t(
          'Enter your order number (trade_no) to verify payment and credit balance'
        )}
        contentClassName="sm:max-w-md"
      >
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="manual-trade-no">
              {t('Order Number (trade_no)')}
            </Label>
            <Input
              id="manual-trade-no"
              placeholder={t('Enter order number...')}
              value={manualTradeNo}
              onChange={(e) => setManualTradeNo(e.target.value)}
              disabled={manualChecking}
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => setManualCheckOpen(false)}
              disabled={manualChecking}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={handleManualCheck}
              disabled={!manualTradeNo.trim() || manualChecking}
              className="gap-2"
            >
              {manualChecking && <Loader2 className="h-4 w-4 animate-spin" />}
              {t('Check Status')}
            </Button>
          </div>
        </div>
      </Dialog>
    </>
  )
}
