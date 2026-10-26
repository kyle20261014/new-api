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
import { createInstance } from 'i18next'
import { describe, expect, it } from 'vitest'

import en from '../locales/en.json'
import fr from '../locales/fr.json'
import ja from '../locales/ja.json'
import ru from '../locales/ru.json'
import vi from '../locales/vi.json'
import zhTW from '../locales/zh-TW.json'
import zh from '../locales/zh.json'

describe('iDataRiver i18n resources regression', () => {
  it('all 7 locale files have only translation namespace at root', () => {
    const locales = [
      { code: 'en', res: en },
      { code: 'zh', res: zh },
      { code: 'zh-TW', res: zhTW },
      { code: 'fr', res: fr },
      { code: 'ja', res: ja },
      { code: 'ru', res: ru },
      { code: 'vi', res: vi },
    ]

    for (const { code, res } of locales) {
      const rootKeys = Object.keys(res)
      expect(
        rootKeys,
        `Locale ${code} should only have 'translation' key at root`
      ).toEqual(['translation'])
      expect(
        typeof (res as { translation: Record<string, string> }).translation,
        `Locale ${code} translation should be an object`
      ).toBe('object')
    }
  })

  it('translates core iDataRiver keys through i18next instance in zh', async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: 'zh',
      fallbackLng: 'en',
      resources: {
        en,
        zh,
        'zh-TW': zhTW,
        fr,
        ja,
        ru,
        vi,
      },
      interpolation: { escapeValue: false },
    })

    expect(i18n.t('Fixed Top-up Packages')).toBe('固定套餐充值')
    expect(i18n.t('iDataRiver Gateway')).toBe('iDataRiver 支付网关')
    expect(i18n.t('Platform Payable Amount')).toBe('平台实付金额')
    expect(i18n.t('iDataRiver Payment')).toBe('iDataRiver 充值支付')
    expect(i18n.t('Choose a payment method to complete your top-up')).toBe(
      '选择支付方式以完成充值'
    )
    expect(i18n.t('Payment confirmed and balance updated!')).toBe(
      '支付已确认，额度已更新！'
    )
    expect(i18n.t('Order refund requires manual review.')).toBe(
      '订单退款需人工审核处理。'
    )
    expect(
      i18n.t(
        'If you cannot open the payment page, please turn off VPN or switch networks.'
      )
    ).toBe('如无法打开支付页面，请关闭代理 VPN 或切换网络。')
  })

  it('translates in all 7 languages without returning raw English key', async () => {
    const i18n = createInstance()
    await i18n.init({
      fallbackLng: 'en',
      resources: {
        en,
        zh,
        'zh-TW': zhTW,
        fr,
        ja,
        ru,
        vi,
      },
    })

    const testKeys = [
      'Fixed Top-up Packages',
      'iDataRiver Gateway',
      'Platform Payable Amount',
      'iDataRiver Payment',
      'Recovered',
      'Manual Required',
    ]

    const languages = ['zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
    for (const lang of languages) {
      await i18n.changeLanguage(lang)
      for (const key of testKeys) {
        const translated = i18n.t(key)
        expect(
          translated,
          `Key '${key}' should be translated in ${lang}`
        ).not.toBe('')
        expect(translated, `Key '${key}' should not equal raw key in ${lang}`).not.toBe(key)
      }
    }
  })
})
