import { useTranslation } from 'react-i18next'

import {
  WebPreview,
  WebPreviewBody,
} from '@/components/ai-elements/web-preview'

import { isolatedPreviewDocument } from './display'

export function DiagnosticPreview(props: { source: string }) {
  const { t } = useTranslation()
  return (
    <section aria-label={t('Artwork preview')}>
      <WebPreview className='h-80 overflow-hidden'>
        <WebPreviewBody
          key={props.source}
          sandbox='allow-scripts'
          {...{ credentialless: '' }}
          srcDoc={isolatedPreviewDocument(props.source)}
          referrerPolicy='no-referrer'
          title={t('Artwork preview')}
          className='h-80 bg-white'
        />
      </WebPreview>
    </section>
  )
}
