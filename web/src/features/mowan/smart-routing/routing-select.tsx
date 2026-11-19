import { Fragment, useId, type ReactNode } from 'react'

import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export function RoutingSelect(props: {
  label: string
  value: string
  options: Array<{
    value: string
    label: string
    description?: string
    badge?: ReactNode
  }>
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const id = useId()
  return (
    <div className='min-w-0 space-y-1.5'>
      <label htmlFor={id} className='text-sm font-medium'>
        {props.label}
      </label>
      <Select
        value={props.value}
        onValueChange={(value) => {
          if (value !== null) props.onChange(value)
        }}
        disabled={props.disabled}
      >
        <SelectTrigger id={id} className='min-h-11 w-full min-w-0'>
          <SelectValue>
            {props.options.find((item) => item.value === props.value)?.badge ??
              props.options.find((item) => item.value === props.value)?.label ??
              props.value}
          </SelectValue>
        </SelectTrigger>
        <SelectContent
          alignItemWithTrigger={!props.options.some((item) => item.description)}
          className='max-w-[calc(100vw-2rem)]'
        >
          {props.options.map((item, index) => (
            <Fragment key={item.value}>
              {index > 0 && item.description && (
                <SelectSeparator className='mx-3 my-0' />
              )}
              <SelectItem
                value={item.value}
                aria-label={item.label}
                className={
                  item.description
                    ? 'min-h-20 py-3 pl-3 [&_[data-slot=select-item-text]]:min-w-0 [&_[data-slot=select-item-text]]:whitespace-normal'
                    : undefined
                }
              >
                <span className='flex min-w-0 flex-col items-start gap-2'>
                  {item.badge ?? item.label}
                  {item.description && (
                    <span className='text-muted-foreground text-xs leading-relaxed'>
                      {item.description}
                    </span>
                  )}
                </span>
              </SelectItem>
            </Fragment>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
