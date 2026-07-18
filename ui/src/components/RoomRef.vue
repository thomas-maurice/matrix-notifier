<script setup>
// Compact room reference: the canonical alias when the room has one (blue),
// the raw ID otherwise (red, default <code> styling). Either way a click
// copies the room ID — the alias is for humans, the ID is what you paste
// into configs and admin tools.
import { notifyError, notifySuccess } from '../toast.js'

const props = defineProps({
  roomId: { type: String, required: true },
  alias: { type: String, default: '' },
})

async function copyId() {
  try {
    await navigator.clipboard.writeText(props.roomId)
    notifySuccess(`Copied ${props.roomId}`)
  } catch (e) {
    notifyError(`Cannot copy to clipboard: ${e.message}`)
  }
}
</script>

<template>
  <code
    v-if="alias"
    class="text-info"
    style="cursor: pointer"
    :title="`${roomId} — click to copy the ID`"
    @click="copyId"
    >{{ alias }}</code
  >
  <code v-else style="cursor: pointer" title="Click to copy the ID" @click="copyId">{{ roomId }}</code>
</template>
