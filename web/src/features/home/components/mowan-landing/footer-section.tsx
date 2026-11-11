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
import { MessageSquare } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

export function FooterSection() {
  const { t } = useTranslation()

  const handlePlaceholderClick = (e: React.MouseEvent, title: string) => {
    e.preventDefault()
    toast(
      t('mowan.placeholder.notice', {
        title,
        defaultValue: `${title}功能即将推出，敬请期待！`,
      })
    )
  }

  return (
    <footer id='footer' data-xc-section='footer' className='xc-footer'>
      <div className='xc-footer-main'>
        {/* Brand Column */}
        <div className='xc-footer-brand'>
          <div className='xc-brand'>
            <img
              src='/images/mowan/mowan-icon.png'
              alt={t('mowan.brand.name', '魔丸AI')}
            />
            <span>{t('mowan.brand.name', '魔丸AI')}</span>
          </div>
          <p>
            {t(
              'mowan.footer.tagline',
              '一个入口，连接全球 AI 创作能力。汇聚前沿多模态大模型，助力每一位创作者释放无限潜能。'
            )}
          </p>
        </div>

        {/* Column 1: Products */}
        <div className='xc-footer-column'>
          <h3>{t('mowan.footer.colProducts', '产品')}</h3>
          <a href='#create'>{t('mowan.create.cat.chat', '智能对话')}</a>
          <a href='#create'>{t('mowan.create.cat.image', '图像创作')}</a>
          <a href='#create'>{t('mowan.create.cat.video', '视频生成')}</a>
          <a href='#voice'>{t('mowan.create.cat.audio', '语音合成')}</a>
          <a href='#models'>{t('mowan.footer.agent', 'AI 智能体')}</a>
        </div>

        {/* Column 2: Open Platform */}
        <div className='xc-footer-column'>
          <h3>{t('mowan.footer.colApi', 'API开放平台')}</h3>
          <a href='/console'>{t('mowan.footer.apiDocs', 'API 文档')}</a>
          <a href='/console'>{t('mowan.footer.modelPricing', '模型价格')}</a>
          <a href='/console'>{t('mowan.footer.apiPlatform', '开发者控制台')}</a>
        </div>

        {/* Column 3: About & Legal */}
        <div className='xc-footer-column'>
          <h3>{t('mowan.footer.colAbout', '关于')}</h3>
          <a
            href='#footer'
            onClick={(e) =>
              handlePlaceholderClick(e, t('mowan.footer.privacy', '隐私政策'))
            }
          >
            {t('mowan.footer.privacy', '隐私政策')}
          </a>
          <a
            href='#footer'
            onClick={(e) =>
              handlePlaceholderClick(e, t('mowan.footer.terms', '用户协议'))
            }
          >
            {t('mowan.footer.terms', '用户协议')}
          </a>
          <a
            href='#footer'
            onClick={(e) =>
              handlePlaceholderClick(e, t('mowan.footer.refund', '退款政策'))
            }
          >
            {t('mowan.footer.refund', '退款政策')}
          </a>
          <a
            href='#footer'
            onClick={(e) =>
              handlePlaceholderClick(e, t('mowan.footer.support', '联系客服'))
            }
          >
            {t('mowan.footer.support', '联系客服')}
          </a>
          <a
            href='#footer'
            onClick={(e) =>
              handlePlaceholderClick(e, t('mowan.footer.business', '商务合作'))
            }
          >
            {t('mowan.footer.business', '商务合作')}
          </a>
        </div>
      </div>

      {/* Footer Bottom Bar */}
      <div className='xc-footer-bottom'>
        <div>
          {t('mowan.footer.copyright', '© 2026 魔丸 AI · 保留所有权利')}
        </div>
      </div>

      {/* Support Floating Action Button */}
      <button
        type='button'
        className='xc-support'
        onClick={(e) =>
          handlePlaceholderClick(e, t('mowan.footer.support', '联系客服'))
        }
        aria-label={t('mowan.footer.support', '联系客服')}
      >
        <MessageSquare className='size-5 text-cyan-400' />
      </button>
    </footer>
  )
}
