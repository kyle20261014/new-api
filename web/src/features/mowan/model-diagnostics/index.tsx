import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Brain, Brush, FileText, Fingerprint } from 'lucide-react'
import { useRef, useState, type ReactElement } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import {
  getDiagnosticCatalog,
  getDiagnosticReports,
  startDiagnostic,
} from './api'
import { diagnosticStateKey } from './display'
import { DiagnosticReportView } from './report'
import {
  isDiagnosticRunning,
  type DiagnosticKind,
  type DiagnosticReport,
  type StartDiagnostic,
} from './types'

const kinds: DiagnosticKind[] = [
  'connection',
  'fingerprint',
  'intelligence',
  'drawing',
]
const icons = {
  connection: Activity,
  fingerprint: Fingerprint,
  intelligence: Brain,
  drawing: Brush,
}

type DiagnosticSelection = {
  group?: string
  kind?: DiagnosticKind
  creative?: boolean
  historyId?: string
}

function DiagnosticWorkspace(props: {
  model: string
  userId: number
  selection: DiagnosticSelection
  onSelectionChange: (selection: DiagnosticSelection) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const {
    group: selectedGroup = '',
    kind = 'connection',
    creative = false,
    historyId,
  } = props.selection
  const [confirm, setConfirm] = useState<StartDiagnostic>()
  const keys = useRef(new Map<string, string>())
  const catalog = useQuery({
    queryKey: ['model-diagnostics', props.userId, props.model, 'catalog'],
    queryFn: ({ signal }) => getDiagnosticCatalog(props.model, signal),
  })
  const groups = catalog.data?.groups ?? []
  const group = groups.includes(selectedGroup)
    ? selectedGroup
    : (groups[0] ?? '')
  const reportsKey = [
    'model-diagnostics',
    props.userId,
    props.model,
    group,
    'reports',
  ]
  const reports = useQuery({
    queryKey: reportsKey,
    queryFn: ({ signal }) => getDiagnosticReports(props.model, group, signal),
    enabled: Boolean(group),
    refetchInterval: (query) =>
      query.state.data?.some(isDiagnosticRunning) ? 1500 : false,
    refetchIntervalInBackground: false,
  })
  const mutation = useMutation({
    mutationFn: startDiagnostic,
    retry: false,
    onSuccess: (report, variables) => {
      const key = [
        'model-diagnostics',
        props.userId,
        variables.model,
        variables.group,
        'reports',
      ]
      queryClient.setQueryData<DiagnosticReport[]>(key, (previous) => [
        report,
        ...(previous ?? []).filter((item) => item.id !== report.id),
      ])
      void queryClient.invalidateQueries({ queryKey: key })
      keys.current.delete(
        `${variables.group}:${variables.kind}:${variables.creative}`
      )
      setConfirm(undefined)
    },
  })
  const allReports = reports.data ?? []
  const visible = allReports.filter(
    (report) =>
      report.kind === kind &&
      report.creative === (kind === 'drawing' && creative)
  )
  const current =
    visible.find((report) => report.id === historyId) ?? visible[0]
  const active = visible.find(isDiagnosticRunning)
  const label = (value: DiagnosticKind): string => {
    if (value === 'connection') return t('Connection test')
    if (value === 'fingerprint') return t('Fingerprint test')
    if (value === 'intelligence') return t('Intelligence test')
    return t('Drawing test')
  }
  const prepare = () => {
    if (
      !group ||
      !catalog.data?.chat_supported ||
      active ||
      mutation.isPending
    ) {
      return
    }
    const scope = `${group}:${kind}:${kind === 'drawing' && creative}`
    let idempotencyKey = keys.current.get(scope)
    if (!idempotencyKey) {
      idempotencyKey = crypto.randomUUID()
      keys.current.set(scope, idempotencyKey)
    }
    setConfirm({
      model: props.model,
      group,
      kind,
      creative: kind === 'drawing' && creative,
      idempotency_key: idempotencyKey,
    })
  }
  if (catalog.isPending) return <LoadingState />
  if (catalog.isError) {
    return <ErrorState onRetry={() => void catalog.refetch()} />
  }
  if (!groups.length) {
    return (
      <EmptyState
        title={t('No eligible groups')}
        description={t(
          'You do not currently have access to this model in any group.'
        )}
      />
    )
  }
  return (
    <div className='flex h-full min-h-0 min-w-0 flex-col gap-4 md:flex-row'>
      <aside className='shrink-0 md:w-40 md:border-r md:pr-4'>
        <details open className='md:h-full'>
          <summary className='text-muted-foreground mb-2 min-h-11 cursor-pointer text-xs font-semibold'>
            {t('Authorized groups')}
          </summary>
          <nav
            aria-label={t('Authorized groups')}
            className='flex max-h-32 flex-wrap gap-2 overflow-x-hidden overflow-y-auto md:max-h-[55dvh] md:flex-col'
          >
            {groups.map((item) => (
              <Button
                key={item}
                variant={item === group ? 'secondary' : 'ghost'}
                aria-pressed={item === group}
                className='min-h-11 max-w-full min-w-0 justify-start md:w-full'
                onClick={() => {
                  props.onSelectionChange({
                    ...props.selection,
                    group: item,
                    historyId: undefined,
                  })
                }}
              >
                <span className='truncate' title={item}>
                  {item}
                </span>
              </Button>
            ))}
          </nav>
        </details>
      </aside>
      <div className='flex min-h-0 min-w-0 flex-1 flex-col'>
        <Tabs
          value={kind}
          onValueChange={(value) => {
            props.onSelectionChange({
              ...props.selection,
              kind: value as DiagnosticKind,
              historyId: undefined,
            })
          }}
          className='min-h-0 flex-1'
        >
          <TabsList
            aria-label={t('Model diagnostics')}
            className='grid w-full grid-cols-2 gap-2 group-data-horizontal/tabs:h-auto lg:grid-cols-4'
          >
            {kinds.map((value) => {
              const Icon = icons[value]
              const latest = allReports.find(
                (report) => report.kind === value && !report.creative
              )
              return (
                <TabsTrigger
                  key={value}
                  value={value}
                  className='min-h-11 min-w-0 flex-wrap px-2 py-2'
                >
                  <Icon className='size-4' aria-hidden='true' />
                  <span className='break-words whitespace-normal'>
                    {label(value)}
                  </span>
                  <span className='text-muted-foreground w-full text-[10px]'>
                    {latest
                      ? t(diagnosticStateKey(latest.state))
                      : t('Not tested')}
                  </span>
                </TabsTrigger>
              )
            })}
          </TabsList>
          {kinds.map((value) => (
            <TabsContent
              key={value}
              value={value}
              className='min-h-0 overflow-y-auto overscroll-contain pt-3'
            >
              <div className='space-y-4'>
                <p className='text-muted-foreground text-xs'>
                  {value === 'drawing' && creative
                    ? t('Private creative report')
                    : t(
                        'Shared with users authorized for this model and group'
                      )}
                </p>
                {reports.isPending && <LoadingState />}
                {reports.isError && (
                  <ErrorState onRetry={() => void reports.refetch()} />
                )}
                {!reports.isPending && !reports.isError && !current && (
                  <EmptyState
                    title={t('Not tested')}
                    description={t(
                      'Start a test to generate a report. Viewing results never starts a paid request.'
                    )}
                    className='min-h-48'
                  />
                )}
                {!reports.isError && current && (
                  <DiagnosticReportView report={current} />
                )}
                {visible.length > 1 && (
                  <details className='rounded-lg border p-3'>
                    <summary className='min-h-11 cursor-pointer text-sm'>
                      {t('Report history')}
                    </summary>
                    <div className='mt-2 flex flex-wrap gap-2'>
                      {visible.map((report) => (
                        <Button
                          variant='outline'
                          key={report.id}
                          className='min-h-11 max-w-full'
                          aria-pressed={current?.id === report.id}
                          onClick={() =>
                            props.onSelectionChange({
                              ...props.selection,
                              historyId: report.id,
                            })
                          }
                        >
                          <span className='truncate'>{report.id}</span>
                        </Button>
                      ))}
                    </div>
                  </details>
                )}
              </div>
            </TabsContent>
          ))}
        </Tabs>
        <div className='mt-4 flex shrink-0 flex-wrap items-center gap-3 border-t pt-4'>
          <TechnicalReports reports={allReports} label={label} />
          {kind === 'drawing' && (
            <label className='flex min-h-11 items-center gap-2 text-sm'>
              <Switch
                checked={creative}
                onCheckedChange={(value) => {
                  props.onSelectionChange({
                    ...props.selection,
                    creative: value,
                    historyId: undefined,
                  })
                }}
              />
              {t('Free creation')}
            </label>
          )}
          {!catalog.data?.chat_supported && (
            <p className='text-muted-foreground text-sm'>
              {t('Only text models support diagnostics.')}
            </p>
          )}
          {catalog.data?.chat_supported && (!current || kind !== 'drawing') && (
            <Button
              className='min-h-11 md:ml-auto'
              disabled={
                Boolean(active) ||
                mutation.isPending ||
                reports.isPending ||
                reports.isError
              }
              onClick={prepare}
            >
              {active ? t('Running') : t('Start test')}
            </Button>
          )}
          <p className='text-muted-foreground w-full text-xs'>
            {t(
              'Tests are billed by actual generation usage. Closing this dialog does not stop a running test.'
            )}
          </p>
        </div>
      </div>
      <ConfirmDialog
        open={Boolean(confirm)}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setConfirm(undefined)
        }}
        title={t('Start model diagnostic?')}
        desc={t(
          'The initiator pays for actual generation usage. Local scoring adds no model calls. This starts one test and will not automatically generate another artwork.'
        )}
        confirmText={t('Start test')}
        isLoading={mutation.isPending}
        handleConfirm={() => {
          if (confirm && !mutation.isPending) mutation.mutate(confirm)
        }}
      />
    </div>
  )
}

function TechnicalReports(props: {
  reports: DiagnosticReport[]
  label: (kind: DiagnosticKind) => string
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      title={t('Technical report')}
      trigger={
        <Button variant='outline' className='min-h-11'>
          <FileText className='size-4' />
          {t('Technical report')}
        </Button>
      }
      contentClassName='sm:max-w-4xl'
      contentHeight='min(70dvh, 700px)'
    >
      <p className='text-muted-foreground mb-4 text-sm'>
        {t(
          'This routing result does not represent every channel in this group.'
        )}
      </p>
      {!props.reports.length && <EmptyState title={t('Not tested')} />}
      <div className='space-y-4'>
        {props.reports.map((report) => (
          <details key={report.id} className='rounded-lg border p-3'>
            <summary className='min-h-11 cursor-pointer text-sm break-all'>
              {props.label(report.kind)} · {report.id} ·{' '}
              {t(diagnosticStateKey(report.state))}
            </summary>
            <div className='mt-4'>
              <DiagnosticReportView report={report} technical />
            </div>
          </details>
        ))}
      </div>
    </Dialog>
  )
}

export function ModelDiagnostics(props: {
  model: string
  trigger?: ReactElement
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [open, setOpen] = useState(false)
  const [selection, setSelection] = useState<{
    scope: string
    value: DiagnosticSelection
  }>()
  const scope = `${user?.id}:${props.model}`
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Model diagnostics')}
      description={props.model}
      descriptionClassName='break-all'
      trigger={
        props.trigger ?? (
          <Button variant='outline' className='min-h-11'>
            <Activity className='size-4' />
            {t('Model diagnostics')}
          </Button>
        )
      }
      contentClassName='sm:max-w-6xl'
      contentHeight='min(76dvh, 850px)'
      bodyClassName='h-full min-h-0'
    >
      {open && user && (
        <DiagnosticWorkspace
          key={`${user.id}:${props.model}`}
          model={props.model}
          userId={user.id}
          selection={selection?.scope === scope ? selection.value : {}}
          onSelectionChange={(value) => setSelection({ scope, value })}
        />
      )}
      {!user && (
        <EmptyState
          title={t('Sign in')}
          description={t(
            'Sign in to view authorized diagnostic reports and start tests.'
          )}
        />
      )}
    </Dialog>
  )
}
