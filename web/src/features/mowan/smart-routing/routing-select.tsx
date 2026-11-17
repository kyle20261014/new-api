import { useId } from 'react'

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

export function RoutingSelect(props: {
  label: string
  value: string
  options: Array<{ value: string; label: string }>
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
            {props.options.find((item) => item.value === props.value)?.label ??
              props.value}
          </SelectValue>
        </SelectTrigger>
        <SelectContent>
          {props.options.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
