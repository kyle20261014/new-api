export interface SubmissionReference {
  submission_id: string
  submission_status: 'accepted' | 'unknown'
}

export function getSubmissionReference(
  value: unknown
): SubmissionReference | undefined {
  if (!value || typeof value !== 'object') return undefined
  const data = value as Record<string, unknown>
  if (
    typeof data.submission_id === 'string' &&
    data.submission_id.length > 0 &&
    (data.submission_status === 'accepted' ||
      data.submission_status === 'unknown')
  ) {
    return {
      submission_id: data.submission_id,
      submission_status: data.submission_status,
    }
  }
  for (const key of ['error', 'data']) {
    const nested = data[key]
    if (nested && typeof nested === 'object') {
      const record = nested as Record<string, unknown>
      if (
        typeof record.submission_id === 'string' &&
        record.submission_id.length > 0 &&
        (record.submission_status === 'accepted' ||
          record.submission_status === 'unknown')
      ) {
        return {
          submission_id: record.submission_id,
          submission_status: record.submission_status,
        }
      }
    }
  }
  return undefined
}
