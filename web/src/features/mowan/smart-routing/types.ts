import { z } from 'zod'

export const dynamicStrategySchema = z.enum([
  'balanced',
  'price',
  'speed',
  'success',
])
const groupSchema = z
  .string()
  .min(1)
  .refine((group) => group !== 'auto' && group !== 'all')
export const modelRoutingSchema = z
  .object({
    groups: z.array(groupSchema).min(1).max(100),
    strategy: z.enum(['balanced', 'price', 'speed', 'success', 'manual']),
    try_soft_offline: z.boolean().default(false),
  })
  .strict()
export const routingSchema = z.union([
  z
    .object({
      schema_version: z.literal(1),
      mode: z.literal('fixed'),
      fixed_group: z.string(),
    })
    .strict(),
  z
    .object({
      schema_version: z.literal(1),
      mode: z.literal('smart'),
      strategy: dynamicStrategySchema,
      excluded_models: z.array(z.string().min(1)).max(200).optional(),
      excluded_groups: z.array(groupSchema).max(100).optional(),
      excluded_model_groups: z
        .record(z.string().min(1), z.array(groupSchema).max(100))
        .refine((models) => Object.keys(models).length <= 200)
        .optional(),
    })
    .strict(),
  z
    .object({
      schema_version: z.literal(1),
      mode: z.literal('smart'),
      strategy: z.literal('custom'),
      models: z
        .record(z.string().min(1), modelRoutingSchema)
        .refine(
          (models) =>
            Object.keys(models).length > 0 && Object.keys(models).length <= 200
        ),
    })
    .strict(),
])
export type RoutingPolicy = z.infer<typeof routingSchema>
export type DynamicStrategy = z.infer<typeof dynamicStrategySchema>
export type ModelRouting = z.infer<typeof modelRoutingSchema>
export type CustomRouting = Extract<RoutingPolicy, { strategy: 'custom' }>
export type DynamicRouting = Extract<
  RoutingPolicy,
  { strategy: DynamicStrategy }
>
export type RoutingHealth =
  | 'normal'
  | 'congested'
  | 'soft_offline'
  | 'unknown'
  | 'degraded'
export type ModelCategory = 'all' | 'text' | 'image' | 'video' | 'audio'
export interface RoutingMetrics {
  protocol_class: string
  response_kind: 'ttft' | 'response' | 'task_terminal'
  response_ms_1h: number | null
  response_samples_1h: number
  success_24h: number | null
  samples_24h: number
  stale: boolean
  updated_at: number | null
  submission_ms_1h?: number | null
}
export interface RoutingPrice {
  label: string
  amount?: number
  min?: number
  max?: number
  unit: string
  rule?: string
}
export interface RoutingCandidate {
  model: string
  display_name: string
  category: Exclude<ModelCategory, 'all'>
  group: string
  available: boolean
  health: RoutingHealth
  quote: {
    comparison: 'available' | 'unavailable'
    reason?: string
    estimated_total?: number
    currency: 'USD'
    prices: RoutingPrice[]
  }
  metrics: RoutingMetrics
  variants?: Array<
    RoutingMetrics & {
      provider_key: string
      operation_key: string
      health: RoutingHealth
    }
  >
  reason?: string
}
export interface CandidatePage {
  items: RoutingCandidate[]
  total: number
  page: number
  page_size: number
}
export interface RoutingPreview extends CandidatePage {
  selected_group?: string
  reason: string
  algorithm_version: string
}
export interface RoutingSubmission {
  submission_id: string
  task_id: string | null
  submission_status:
    | 'accepted'
    | 'unknown'
    | 'prepared'
    | 'sending'
    | 'not_sent'
    | 'rejected'
  reserved_amount: number
  reserved_unit: string
  updated_at: number
  funding_status: 'pending' | 'manual_review' | 'settled' | 'released'
  model: string
  group: string
  submit_time: number
}
