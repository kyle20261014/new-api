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

import {
  defaultRouting,
  routingIsValid,
} from '@/features/mowan/smart-routing/policy'
import { RoutingControl } from '@/features/mowan/smart-routing/routing-control'
import type { RoutingPolicy } from '@/features/mowan/smart-routing/types'

import { PlaygroundChat } from './components/chat/playground-chat'
import { PlaygroundInput } from './components/input/playground-input'
import {
  useChatHandler,
  usePlaygroundConversation,
  usePlaygroundOptions,
  usePlaygroundState,
} from './hooks'
import { buildChatCompletionPayload } from './lib'

export function Playground(props: { initialModel?: string } = {}) {
  const { t } = useTranslation()
  const [routing, setRouting] = useState<RoutingPolicy>(defaultRouting)
  const {
    config,
    parameterEnabled,
    messages,
    isLoadingMessages,
    models,
    groups,
    updateMessages,
    setModels,
    setGroups,
    updateConfig,
    updateParameterEnabled,
    clearMessages,
  } = usePlaygroundState(props.initialModel)

  const { sendChat, stopGeneration, isGenerating } = useChatHandler({
    config,
    parameterEnabled,
    onMessageUpdate: updateMessages,
    routing,
  })

  const {
    editingMessageKey,
    handleSendMessage,
    handleRegenerateMessage,
    handleEditMessage,
    handleEditOpenChange,
    applyEdit,
    handleDeleteMessage,
  } = usePlaygroundConversation({
    messages,
    updateMessages,
    sendChat,
  })

  const handleClearMessages = () => {
    handleEditOpenChange(false)
    clearMessages()
  }

  const { isLoadingModels } = usePlaygroundOptions({
    currentGroup: config.group,
    routingGroup: routing.mode === 'fixed' ? routing.fixed_group : '',
    preserveUnavailableModel:
      !!props.initialModel && config.model === props.initialModel,
    currentModel: config.model,
    setGroups,
    setModels,
    updateConfig,
  })
  const eligibleModels = models.filter((model) => {
    if (routing.mode === 'fixed') return true
    if (routing.strategy === 'custom') return !!routing.models[model.value]
    return !routing.excluded_models?.includes(model.value)
  })
  const routingUnavailable =
    !routingIsValid(routing) ||
    !eligibleModels.some((model) => model.value === config.model)

  return (
    <div className='relative flex size-full min-h-0 flex-col overflow-hidden'>
      {/* Full-width scroll container: scrolling works even over side whitespace */}
      <div className='flex min-h-0 flex-1 flex-col overflow-hidden'>
        <PlaygroundChat
          messages={messages}
          isLoadingMessages={isLoadingMessages}
          onRegenerateMessage={handleRegenerateMessage}
          onEditMessage={handleEditMessage}
          onDeleteMessage={handleDeleteMessage}
          onSelectPrompt={handleSendMessage}
          isGenerating={isGenerating}
          editingKey={editingMessageKey}
          onCancelEdit={handleEditOpenChange}
          onSaveEdit={(newContent) => applyEdit(newContent, false)}
          onSaveEditAndSubmit={(newContent) => applyEdit(newContent, true)}
        />
      </div>

      {/* Input area: center content and constrain to the same container width */}
      <div className='mx-auto w-full max-w-4xl'>
        <details className='bg-background max-h-[50dvh] overflow-auto border-t px-4 py-2'>
          <summary className='min-h-11 cursor-pointer text-sm font-medium'>
            {t('Temporary routing for this experience')}
          </summary>
          <p className='text-muted-foreground mb-3 text-xs'>
            {t(
              'Applies only to these requests. No saved key is changed. Actual account billing still applies.'
            )}
          </p>
          <RoutingControl
            value={routing}
            onChange={setRouting}
            groups={groups.map((group) => group.value)}
            model={config.model}
            disabled={isGenerating}
            previewRequest={{
              ...buildChatCompletionPayload(messages, config, parameterEnabled),
            }}
          />
        </details>
        {routingUnavailable && !isLoadingModels && (
          <p role='status' className='text-muted-foreground px-4 py-2 text-sm'>
            {t(
              'Select at least one model and group, and fix invalid routing selections.'
            )}
          </p>
        )}
        <PlaygroundInput
          config={config}
          disabled={isGenerating}
          submitDisabled={routingUnavailable}
          groups={groups}
          hideGroup
          groupValue={config.group}
          isGenerating={isGenerating}
          isModelLoading={isLoadingModels}
          modelValue={config.model}
          models={eligibleModels}
          onGroupChange={(value) => updateConfig('group', value)}
          onConfigChange={updateConfig}
          onClearMessages={handleClearMessages}
          onModelChange={(value) => updateConfig('model', value)}
          onParameterEnabledChange={updateParameterEnabled}
          onStop={stopGeneration}
          onSubmit={(text) => {
            if (!routingUnavailable) handleSendMessage(text)
          }}
          parameterEnabled={parameterEnabled}
          hasMessages={messages.length > 0}
        />
      </div>
    </div>
  )
}
