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

import { NAV_SECTIONS } from './data'

export interface SectionNavProps {
  activeSection: string
  onSectionClick: (sectionId: string) => void
}

export function SectionNav(props: SectionNavProps) {
  const { t } = useTranslation()

  return (
    <nav
      aria-label={t('mowan.nav.sectionNavLabel', '分节导航')}
      className='xc-dots'
    >
      {NAV_SECTIONS.map((sec) => {
        const isActive = props.activeSection === sec.id
        return (
          <button
            key={sec.id}
            type='button'
            onClick={() => props.onSectionClick(sec.id)}
            aria-current={isActive ? 'true' : undefined}
            className={isActive ? 'active' : ''}
          >
            <span>{t(sec.labelKey, sec.id)}</span>
            <i />
          </button>
        )
      })}
    </nav>
  )
}
