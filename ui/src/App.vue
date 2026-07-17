<script setup>
import { ref, onMounted } from 'vue'
import { api } from './api.js'
import StatusPanel from './components/StatusPanel.vue'
import ChannelsPanel from './components/ChannelsPanel.vue'
import TokensPanel from './components/TokensPanel.vue'
import SettingsPanel from './components/SettingsPanel.vue'

const authed = ref(false)
const tab = ref('status')
const loginPassword = ref('')
const loginError = ref('')

async function tryLogin() {
  loginError.value = ''
  try {
    // Login sets the httpOnly session cookie; the token never touches JS.
    await api.login(loginPassword.value)
    authed.value = true
    loginPassword.value = ''
  } catch (e) {
    loginError.value = e.status === 401 ? 'Invalid password' : `Cannot reach the bot: ${e.message}`
  }
}

async function logout() {
  try {
    await api.logout()
  } catch {
    // The cookie may already be dead; drop to the login screen regardless.
  }
  authed.value = false
  tab.value = 'status'
}

onMounted(async () => {
  // An existing session cookie (7d validity) survives page reloads.
  try {
    await api.getStatus()
    authed.value = true
  } catch {
    // no valid session; show the login form
  }
})
</script>

<template>
  <nav class="navbar navbar-expand border-bottom mb-4">
    <div class="container">
      <span class="navbar-brand">
        <i class="fa-solid fa-bell me-2 text-primary"></i>matrix-notifier
      </span>
      <ul v-if="authed" class="navbar-nav me-auto">
        <li class="nav-item">
          <a class="nav-link" :class="{ active: tab === 'status' }" href="#" @click.prevent="tab = 'status'">
            <i class="fa-solid fa-heart-pulse me-1"></i>Status
          </a>
        </li>
        <li class="nav-item">
          <a class="nav-link" :class="{ active: tab === 'channels' }" href="#" @click.prevent="tab = 'channels'">
            <i class="fa-solid fa-hashtag me-1"></i>Channels
          </a>
        </li>
        <li class="nav-item">
          <a class="nav-link" :class="{ active: tab === 'tokens' }" href="#" @click.prevent="tab = 'tokens'">
            <i class="fa-solid fa-key me-1"></i>Tokens
          </a>
        </li>
        <li class="nav-item">
          <a class="nav-link" :class="{ active: tab === 'settings' }" href="#" @click.prevent="tab = 'settings'">
            <i class="fa-solid fa-gear me-1"></i>Settings
          </a>
        </li>
      </ul>
      <button v-if="authed" class="btn btn-outline-secondary btn-sm" @click="logout">
        <i class="fa-solid fa-right-from-bracket me-1"></i>Logout
      </button>
    </div>
  </nav>

  <div class="container" style="max-width: 960px">
    <div v-if="!authed" class="row justify-content-center mt-5">
      <div class="col-md-6">
        <div class="card">
          <div class="card-body">
            <h5 class="card-title mb-3"><i class="fa-solid fa-lock me-2"></i>Admin login</h5>
            <form @submit.prevent="tryLogin">
              <div class="mb-3">
                <input
                  v-model="loginPassword"
                  type="password"
                  class="form-control"
                  placeholder="Admin password"
                  autofocus
                />
              </div>
              <div v-if="loginError" class="alert alert-danger py-2">{{ loginError }}</div>
              <button class="btn btn-primary w-100" type="submit">Sign in</button>
            </form>
          </div>
        </div>
      </div>
    </div>

    <template v-else>
      <StatusPanel v-if="tab === 'status'" />
      <ChannelsPanel v-if="tab === 'channels'" />
      <TokensPanel v-if="tab === 'tokens'" />
      <SettingsPanel v-if="tab === 'settings'" />
    </template>
  </div>
</template>
