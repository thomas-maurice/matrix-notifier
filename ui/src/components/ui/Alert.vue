<script setup lang="ts">
import { cn } from '../../lib/utils'

const props = withDefaults(
  defineProps<{ variant?: 'default' | 'destructive' | 'warning' }>(),
  { variant: 'default' },
)

const variants = {
  default: 'bg-card text-card-foreground',
  destructive: 'border-red-500/30 bg-red-500/10 text-red-400',
  warning: 'border-amber-500/30 bg-amber-500/10 text-amber-200',
}
</script>

<template>
  <div :class="cn('rounded-lg border px-4 py-3 text-sm', variants[props.variant])">
    <slot />
  </div>
</template>
