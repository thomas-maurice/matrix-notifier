<script setup lang="ts">
import { watch, onUnmounted } from 'vue'
import { X } from '@lucide/vue'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

watch(
  () => props.open,
  (open) => {
    if (open) window.addEventListener('keydown', onKeydown)
    else window.removeEventListener('keydown', onKeydown)
  },
)
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div class="absolute inset-0 bg-black/70" @click="emit('close')"></div>
      <div
        class="relative w-full max-w-md rounded-xl border bg-card p-5 text-card-foreground shadow-lg"
        role="dialog"
        aria-modal="true"
      >
        <div class="mb-4 flex items-center justify-between">
          <h3 class="text-sm font-semibold"><slot name="title" /></h3>
          <button
            type="button"
            class="cursor-pointer text-muted-foreground transition-colors hover:text-foreground"
            aria-label="Close"
            @click="emit('close')"
          >
            <X class="size-4" />
          </button>
        </div>
        <slot />
      </div>
    </div>
  </Teleport>
</template>
