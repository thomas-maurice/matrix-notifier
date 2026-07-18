<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, errMsg } from '../api'
import { notifyError, notifySuccess } from '../toast'

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
  <div class="row justify-content-center">
    <div class="col-md-6">
      <div class="card mb-4">
        <div class="card-header"><i class="fa-solid fa-id-badge me-2"></i>Bot profile</div>
        <div class="card-body">
          <form @submit.prevent="saveProfile">
            <div class="d-flex align-items-center mb-3">
              <img
                v-if="avatarPreview"
                :src="avatarPreview"
                alt="Bot avatar"
                class="rounded-circle me-3"
                style="width: 64px; height: 64px; object-fit: cover"
              />
              <div
                v-else
                class="rounded-circle me-3 bg-body-tertiary d-flex align-items-center justify-content-center"
                style="width: 64px; height: 64px"
              >
                <i class="fa-solid fa-robot text-secondary"></i>
              </div>
              <div class="flex-grow-1">
                <label class="form-label" for="botDisplayName">Display name</label>
                <input
                  id="botDisplayName"
                  v-model="displayName"
                  class="form-control"
                  placeholder="Notifier"
                />
              </div>
            </div>
            <div class="mb-3">
              <label class="form-label" for="botAvatar">Avatar image</label>
              <input
                id="botAvatar"
                type="file"
                class="form-control"
                accept="image/*"
                @change="pickAvatar"
              />
            </div>
            <button class="btn btn-primary" type="submit" :disabled="savingProfile">
              <i class="fa-solid fa-floppy-disk me-1"></i>Save profile
            </button>
            <div class="form-text mt-2">
              Name and picture are the bot's Matrix profile, visible in every room it is in.
            </div>
          </form>
        </div>
      </div>

      <div class="card">
        <div class="card-header"><i class="fa-solid fa-key me-2"></i>Change admin password</div>
        <div class="card-body">
          <form @submit.prevent="changePassword">
            <div class="mb-3">
              <label class="form-label" for="currentPassword">Current password</label>
              <input
                id="currentPassword"
                v-model="currentPassword"
                type="password"
                class="form-control"
                autocomplete="current-password"
                required
              />
            </div>
            <div class="mb-3">
              <label class="form-label" for="newPassword">New password</label>
              <input
                id="newPassword"
                v-model="newPassword"
                type="password"
                class="form-control"
                autocomplete="new-password"
                minlength="8"
                required
              />
            </div>
            <div class="mb-3">
              <label class="form-label" for="confirmPassword">Confirm new password</label>
              <input
                id="confirmPassword"
                v-model="confirmPassword"
                type="password"
                class="form-control"
                autocomplete="new-password"
                minlength="8"
                required
              />
            </div>
            <button class="btn btn-primary" type="submit">Change password</button>
            <div class="form-text mt-2">
              Changing the password logs out every other session (browsers and API tokens alike).
            </div>
          </form>
        </div>
      </div>
    </div>
  </div>
</template>
