import {
  CheckListIcon,
  Coins01Icon,
  FlashIcon,
  Shield01Icon,
  SparklesIcon,
} from '@hugeicons/core-free-icons'
import { useTranslation } from 'react-i18next'

export function useRoutingStrategies() {
  const { t } = useTranslation()
  return [
    {
      value: 'custom',
      label: t('Custom per model'),
      description: t('Choose the groups and order to use for each model.'),
      icon: CheckListIcon,
      color: 'bg-fuchsia-500/10 text-fuchsia-700 dark:text-fuchsia-300',
    },
    {
      value: 'balanced',
      label: t('Balanced'),
      description: t(
        'Balances price, speed and reliability. Choose this if you are unsure.'
      ),
      icon: SparklesIcon,
      color: 'bg-violet-500/10 text-violet-700 dark:text-violet-300',
    },
    {
      value: 'price',
      label: t('Price first'),
      description: t(
        'Prefers lower-priced groups when prices can be compared.'
      ),
      icon: Coins01Icon,
      color: 'bg-amber-500/10 text-amber-800 dark:text-amber-300',
    },
    {
      value: 'speed',
      label: t('Speed first'),
      description: t('Prefers groups that have responded faster recently.'),
      icon: FlashIcon,
      color: 'bg-cyan-500/10 text-cyan-800 dark:text-cyan-300',
    },
    {
      value: 'success',
      label: t('Success first'),
      description: t('Prefers groups with more reliable recent calls.'),
      icon: Shield01Icon,
      color: 'bg-emerald-500/10 text-emerald-800 dark:text-emerald-300',
    },
  ]
}
