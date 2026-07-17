<script setup>
import { ref } from 'vue'
import { api } from '../api.js'
import { notifyError, notifySuccess } from '../toast.js'

const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')

async function changePassword() {
  if (newPassword.value !== confirmPassword.value) {
    notifyError('New passwords do not match')
    return
  }
  try {
    // Rotates the JWT secret: every other session is logged out; ours is
    // kept alive by the fresh cookie the response sets.
    await api.changePassword(currentPassword.value, newPassword.value)
    notifySuccess('Password changed — all other sessions have been logged out')
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
  } catch (e) {
    notifyError(e.message)
  }
}
</script>

<template>
  <div class="row justify-content-center">
    <div class="col-md-6">
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
