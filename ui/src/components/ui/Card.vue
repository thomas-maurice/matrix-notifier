<script setup lang="ts">
// shadcn Card collapsed into one component: an optional #header slot renders
// the bordered header row; `flush` drops the body padding so tables can run
// edge to edge.
withDefaults(defineProps<{ flush?: boolean }>(), { flush: false })
</script>

<template>
  <div class="rounded-xl border bg-card text-card-foreground shadow-sm">
    <div
      v-if="$slots.header"
      class="flex items-center gap-2 border-b px-4 py-3 text-sm font-medium [&>svg]:size-4 [&>svg]:text-muted-foreground"
    >
      <slot name="header" />
    </div>
    <div :class="flush ? 'overflow-x-auto' : 'p-4'">
      <slot />
    </div>
  </div>
</template>
