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
import type { ReactNode } from 'react'

export interface ModelCapsule {
  id: string
  name: string
  category: 'chat' | 'image' | 'video' | 'audio' | 'code'
  tag?: string
  tagKey?: string
  icon?: string | ReactNode
  color?: string
}

export interface GalleryItem {
  id: string
  index: number
  titleKey: string
  titleFallback: string
  tagKey: string
  subKey: string
  image: string
  width: number
  height: number
  aspectRatio: string
  generated: boolean
  replacement?: string
}

export interface NavSection {
  id: string
  labelKey: string
  href: string
}
