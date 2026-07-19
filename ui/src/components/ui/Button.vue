<script setup lang="ts">
import { cn } from '../../lib/utils'

const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'secondary' | 'outline' | 'ghost' | 'destructive'
    size?: 'default' | 'sm' | 'icon' | 'icon-sm'
    type?: 'button' | 'submit'
  }>(),
  { variant: 'default', size: 'default', type: 'button' },
)

const variants = {
  default: 'bg-primary text-primary-foreground shadow-sm hover:bg-primary/90',
  secondary: 'bg-secondary text-secondary-foreground shadow-sm hover:bg-secondary/80',
  outline: 'border border-input bg-transparent shadow-sm hover:bg-accent hover:text-accent-foreground',
  ghost: 'hover:bg-accent hover:text-accent-foreground',
  destructive: 'bg-destructive/15 text-red-400 shadow-sm hover:bg-destructive/25',
}

const sizes = {
  default: 'h-9 px-4 py-2',
  sm: 'h-8 rounded-md px-3 text-xs',
  icon: 'size-9',
  'icon-sm': 'size-8',
}
</script>

<template>
  <button
    :type="props.type"
    :class="
      cn(
        'inline-flex cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0',
        variants[props.variant],
        sizes[props.size],
      )
    "
  >
    <slot />
  </button>
</template>
