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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getUserGroups, getUserModels } from '../api'
import {
  getGroupFallback,
  getModelFallback,
  getOptionLoadErrorMessage,
  shouldClearModelForGroup,
  sortGroupsByRatio,
} from '../lib'
import type { GroupOption, ModelOption, PlaygroundConfig } from '../types'

type UsePlaygroundOptionsParams = {
  currentGroup: string
  currentModel: string
  targetModel?: string
  setGroups: (groups: GroupOption[]) => void
  setModels: (models: ModelOption[]) => void
  updateConfig: <K extends keyof PlaygroundConfig>(
    key: K,
    value: PlaygroundConfig[K]
  ) => void
}

export function usePlaygroundOptions({
  currentGroup,
  currentModel,
  targetModel,
  setGroups,
  setModels,
  updateConfig,
}: UsePlaygroundOptionsParams) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const resolvingTargetRef = useRef<string | null>(null)

  const {
    data: modelsData,
    error: modelsError,
    isError: isModelsError,
    isLoading: isLoadingModels,
  } = useQuery({
    queryKey: ['playground-models', currentGroup],
    queryFn: async () =>
      requireServerSuccess(await getUserModels(currentGroup)),
    enabled: currentGroup !== '',
  })

  const {
    data: groupsData,
    error: groupsError,
    isError: isGroupsError,
  } = useQuery({
    queryKey: ['playground-groups'],
    queryFn: async () => requireServerSuccess(await getUserGroups()),
  })

  useEffect(() => {
    if (!isModelsError) return

    handleServerError(
      modelsError,
      getOptionLoadErrorMessage(
        modelsError,
        t('Failed to load playground models')
      )
    )
  }, [isModelsError, modelsError, t])

  useEffect(() => {
    if (!isGroupsError) return

    handleServerError(
      groupsError,
      getOptionLoadErrorMessage(
        groupsError,
        t('Failed to load playground groups')
      )
    )
  }, [isGroupsError, groupsError, t])

  // Resolve target model pre-selection (A4)
  useEffect(() => {
    const trimmedTarget = targetModel?.trim()
    if (!trimmedTarget || resolvingTargetRef.current === trimmedTarget) return
    if (!groupsData) return

    if (groupsData.length === 0) {
      resolvingTargetRef.current = trimmedTarget
      toast.error(
        t('Model {{model}} is not available for your account', {
          model: trimmedTarget,
        })
      )
      return
    }

    let cancelled = false
    const resolve = async () => {
      try {
        // 1. If current playground group is usable and supports target model, retain it
        const currentGroupOption = groupsData.find((g) => g.value === currentGroup)
        if (currentGroupOption && currentGroup !== '') {
          const currentModels = await queryClient.fetchQuery({
            queryKey: ['playground-models', currentGroup],
            queryFn: async () =>
              requireServerSuccess(await getUserModels(currentGroup)),
          })
          if (cancelled) return
          if (
            Array.isArray(currentModels) &&
            currentModels.some((m) => m.value === trimmedTarget)
          ) {
            resolvingTargetRef.current = trimmedTarget
            updateConfig('model', trimmedTarget)
            return
          }
        }

        // 2. Otherwise select lowest ratio group available to the account supporting target model
        const candidateGroups = sortGroupsByRatio(groupsData).filter(
          (g) => g.value !== currentGroup
        )

        for (const candidate of candidateGroups) {
          const candidateModels = await queryClient.fetchQuery({
            queryKey: ['playground-models', candidate.value],
            queryFn: async () =>
              requireServerSuccess(await getUserModels(candidate.value)),
          })
          if (cancelled) return
          if (
            Array.isArray(candidateModels) &&
            candidateModels.some((m) => m.value === trimmedTarget)
          ) {
            resolvingTargetRef.current = trimmedTarget
            updateConfig('group', candidate.value)
            updateConfig('model', trimmedTarget)
            return
          }
        }

        // 3. Target model not available in any usable group
        resolvingTargetRef.current = trimmedTarget
        toast.error(
          t('Model {{model}} is not available for your account', {
            model: trimmedTarget,
          })
        )
      } catch (err) {
        if (cancelled) return
        resolvingTargetRef.current = trimmedTarget
        handleServerError(err, t('Failed to load playground models'))
      }
    }

    resolve()

    return () => {
      cancelled = true
    }
  }, [targetModel, groupsData, currentGroup, queryClient, updateConfig, t])

  useEffect(() => {
    if (!modelsData) return

    setModels(modelsData)

    // Do not run default model fallback if targetModel was requested (A4)
    if (targetModel?.trim()) {
      return
    }

    const fallback = getModelFallback(modelsData, currentModel)

    if (fallback) {
      updateConfig('model', fallback)
      return
    }

    if (shouldClearModelForGroup(modelsData, currentModel)) {
      updateConfig('model', '')
    }
  }, [modelsData, currentModel, targetModel, setModels, updateConfig])

  useEffect(() => {
    if (!groupsData) return

    setGroups(groupsData)

    // Do not run default group fallback if targetModel was requested (A4)
    if (targetModel?.trim()) {
      return
    }

    const fallback = getGroupFallback(groupsData, currentGroup)

    if (fallback) {
      updateConfig('group', fallback)
    }
  }, [groupsData, currentGroup, targetModel, setGroups, updateConfig])

  return {
    isLoadingModels,
  }
}
