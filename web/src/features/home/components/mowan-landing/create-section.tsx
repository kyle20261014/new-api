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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function CreateSection() {
  const { t } = useTranslation()
  const [feedbackMessage, setFeedbackMessage] = useState<string | null>(null)

  const capabilities = [
    { id: 'chat', labelKey: 'mowan.create.cat.chat', defaultLabel: '智能对话' },
    {
      id: 'image',
      labelKey: 'mowan.create.cat.image',
      defaultLabel: '图像创作',
    },
    {
      id: 'video',
      labelKey: 'mowan.create.cat.video',
      defaultLabel: '视频生成',
    },
    {
      id: 'audio',
      labelKey: 'mowan.create.cat.audio',
      defaultLabel: '语音合成',
    },
  ] as const

  const triggerFeedback = (title?: string) => {
    const finalTitle = title || t('mowan.create.title', '开始创作')
    setFeedbackMessage(
      t('mowan.placeholder.notice', {
        title: finalTitle,
        defaultValue: `${finalTitle}功能即将推出，敬请期待！`,
      })
    )
    setTimeout(() => setFeedbackMessage(null), 2500)
  }

  return (
    <section
      id='create'
      data-xc-section='create'
      className='xc-section xc-prompt'
    >
      <div className='xc-prompt-copy'>
        <div className='xc-eyebrow'>
          <span className='xc-eyebrow-dot' />
          {t('mowan.create.sectionTag', '02 / 开始创作')}
        </div>
        <h2>{t('mowan.create.title', '说出你想要的')}</h2>
        <p className='xc-lead'>
          {t(
            'mowan.create.subtitle',
            '写下一句话，挑一个模型，让创作从这里开始。'
          )}
        </p>
      </div>

      <Button
        type='button'
        variant='ghost'
        onClick={() => triggerFeedback(t('mowan.create.title', '开始创作'))}
        className='xc-capsule'
        aria-label={t('mowan.create.placeholder', '进入工作台，写下你的灵感')}
      >
        <span>✦</span>
        <span>{t('mowan.create.placeholder', '进入工作台，写下你的灵感')}</span>
        <span className='xc-send'>→</span>
      </Button>

      <div className='xc-capabilities'>
        {capabilities.map((cap) => (
          <Button
            key={cap.id}
            type='button'
            variant='ghost'
            onClick={() => triggerFeedback(t(cap.labelKey, cap.defaultLabel))}
            className='xc-glass'
          >
            <span className='xc-dot' />
            {t(cap.labelKey, cap.defaultLabel)}
          </Button>
        ))}
      </div>

      {feedbackMessage && (
        <div role='status' aria-live='polite' className='xc-prompt-feedback'>
          {feedbackMessage}
        </div>
      )}
    </section>
  )
}
