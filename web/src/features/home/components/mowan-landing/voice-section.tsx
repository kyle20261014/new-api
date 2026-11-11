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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export interface VoiceSectionProps {
  onCreateClick?: () => void
}

export function VoiceSection(props: VoiceSectionProps) {
  const { t } = useTranslation()

  const handleStartVoice = () => {
    if (props.onCreateClick) {
      props.onCreateClick()
    } else {
      const el = document.querySelector<HTMLElement>('#create')
      if (el) {
        el.scrollIntoView({ behavior: 'smooth' })
      }
    }
  }

  return (
    <section id='voice' data-xc-section='voice' className='xc-section xc-voice'>
      <div>
        <p className='xc-eyebrow'>{t('mowan.voice.sectionTag', '04 / 声音')}</p>
        <h2>{t('mowan.voice.title', '声音，也能被创造')}</h2>
        <p className='xc-lead'>
          {t(
            'mowan.voice.subtitle',
            '智能拟真，音色由你定义。让每一次发声，都饱含情感与细节。'
          )}
        </p>
        <div className='xc-buttons'>
          <span className='xc-glass'>
            {t('mowan.create.cat.audio', '语音合成')}
          </span>
          <span className='xc-glass'>
            {t('mowan.voice.tabMusic', '音乐生成')}
          </span>
        </div>
        <Button type='button' onClick={handleStartVoice} className='xc-outline'>
          {t('mowan.voice.cta', '立即体验')} →
        </Button>
      </div>
    </section>
  )
}
