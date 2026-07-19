<script setup lang="ts">
import { cn } from '../../lib/utils'

const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'secondary' | 'outline' | 'success' | 'destructive' | 'warning'
  }>(),
  { variant: 'default' },
)

const variants = {
  default: 'border-transparent bg-primary text-primary-foreground',
  secondary: 'border-transparent bg-secondary text-secondary-foreground',
  outline: 'text-foreground',
  success: 'border-transparent bg-green-500/15 text-green-400',
  destructive: 'border-transparent bg-red-500/15 text-red-400',
  warning: 'border-transparent bg-amber-500/15 text-amber-400',
}
</script>

<template>
  <span
    :class="
      cn(
        'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap [&_svg]:size-3',
        variants[props.variant],
      )
    "
  >
    <slot />
  </span>
</template>
