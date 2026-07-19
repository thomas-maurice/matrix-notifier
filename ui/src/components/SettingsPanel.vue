<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { IdCard, KeyRound, Bot, Save } from '@lucide/vue'
import { api, errMsg } from '../api'
import { notifyError, notifySuccess } from '../toast'
import Card from './ui/Card.vue'
import Button from './ui/Button.vue'
import Input from './ui/Input.vue'

const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')

const displayName = ref('')
const avatarPreview = ref('') // object URL of the current or picked avatar
const avatarBytes = ref<Uint8Array | null>(null) // picked file, pending upload
const savingProfile = ref(false)

function setPreview(blob: Blob) {
  if (avatarPreview.value) URL.revokeObjectURL(avatarPreview.value)
  avatarPreview.value = URL.createObjectURL(blob)
}

async function loadProfile() {
  try {
    const p = await api.getProfile({})
    displayName.value = p.displayName
    if (p.avatar.length) {
      setPreview(new Blob([p.avatar as BlobPart], { type: p.avatarMime || 'image/png' }))
    }
  } catch (e) {
    notifyError(`Cannot load bot profile: ${errMsg(e)}`)
  }
}

async function pickAvatar(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  if (file.size > 1024 * 1024) {
    notifyError('Avatar must be under 1 MiB')
    input.value = ''
    return
  }
  avatarBytes.value = new Uint8Array(await file.arrayBuffer())
  setPreview(file)
}

async function saveProfile() {
  savingProfile.value = true
  try {
    await api.setProfile({
      displayName: displayName.value,
      avatar: avatarBytes.value ?? new Uint8Array(),
    })
    avatarBytes.value = null
    notifySuccess('Bot profile updated')
    await loadProfile()
  } catch (e) {
    notifyError(errMsg(e))
  } finally {
    savingProfile.value = false
  }
}

onMounted(loadProfile)

async function changePassword() {
  if (newPassword.value !== confirmPassword.value) {
    notifyError('New passwords do not match')
    return
  }
  try {
    // Rotates the JWT secret: every other session is logged out; ours is
    // kept alive by the fresh cookie the response sets.
    await api.changeAdminPassword({ currentPassword: currentPassword.value, newPassword: newPassword.value })
    notifySuccess('Password changed — all other sessions have been logged out')
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
  } catch (e) {
    notifyError(errMsg(e))
  }
}
</script>

<template>
  <div class="mx-auto w-full max-w-2xl">
    <Card class="mb-4">
      <template #header><IdCard />Bot profile</template>
      <form @submit.prevent="saveProfile">
        <div class="mb-4 flex items-center gap-4">
          <img
            v-if="avatarPreview"
            :src="avatarPreview"
            alt="Bot avatar"
            class="size-16 rounded-full object-cover"
          />
          <div v-else class="flex size-16 items-center justify-center rounded-full bg-muted">
            <Bot class="size-6 text-muted-foreground" />
          </div>
          <div class="flex-1 space-y-1.5">
            <label class="text-sm font-medium" for="botDisplayName">Display name</label>
            <Input id="botDisplayName" v-model="displayName" placeholder="Notifier" />
          </div>
        </div>
        <div class="mb-4 space-y-1.5">
          <label class="text-sm font-medium" for="botAvatar">Avatar image</label>
          <input
            id="botAvatar"
            type="file"
            accept="image/*"
            class="flex h-9 w-full cursor-pointer rounded-md border border-input bg-transparent px-3 py-1.5 text-sm shadow-sm file:me-3 file:border-0 file:bg-transparent file:p-0 file:text-sm file:font-medium file:text-foreground"
            @change="pickAvatar"
          />
        </div>
        <Button type="submit" :disabled="savingProfile"><Save />Save profile</Button>
        <p class="mt-3 text-xs text-muted-foreground">
          Name and picture are the bot's Matrix profile, visible in every room it is in.
        </p>
      </form>
    </Card>

    <Card>
      <template #header><KeyRound />Change admin password</template>
      <form class="space-y-4" @submit.prevent="changePassword">
        <div class="space-y-1.5">
          <label class="text-sm font-medium" for="currentPassword">Current password</label>
          <Input
            id="currentPassword"
            v-model="currentPassword"
            type="password"
            autocomplete="current-password"
            required
          />
        </div>
        <div class="space-y-1.5">
          <label class="text-sm font-medium" for="newPassword">New password</label>
          <Input id="newPassword" v-model="newPassword" type="password" autocomplete="new-password" minlength="8" required />
        </div>
        <div class="space-y-1.5">
          <label class="text-sm font-medium" for="confirmPassword">Confirm new password</label>
          <Input
            id="confirmPassword"
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            minlength="8"
            required
          />
        </div>
        <Button type="submit">Change password</Button>
        <p class="text-xs text-muted-foreground">
          Changing the password logs out every other session (browsers and API tokens alike).
        </p>
      </form>
    </Card>
  </div>
</template>
