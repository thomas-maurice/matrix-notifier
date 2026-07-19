<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { Bell, Activity, Hash, KeyRound, History, Settings, BookOpen, LogOut, Lock } from '@lucide/vue'
import { api, errMsg, isUnauthenticated } from './api'
import Button from './components/ui/Button.vue'
import Card from './components/ui/Card.vue'
import Alert from './components/ui/Alert.vue'
import Input from './components/ui/Input.vue'
import StatusPanel from './components/StatusPanel.vue'
import ChannelsPanel from './components/ChannelsPanel.vue'
import TokensPanel from './components/TokensPanel.vue'
import HistoryPanel from './components/HistoryPanel.vue'
import SettingsPanel from './components/SettingsPanel.vue'
import DocsPanel from './components/DocsPanel.vue'

const authed = ref(false)
const tab = ref('status')
const loginPassword = ref('')
const loginError = ref('')

const tabs = [
  { id: 'status', label: 'Status', icon: Activity },
  { id: 'channels', label: 'Channels', icon: Hash },
  { id: 'tokens', label: 'Tokens', icon: KeyRound },
  { id: 'history', label: 'History', icon: History },
  { id: 'settings', label: 'Settings', icon: Settings },
  { id: 'docs', label: 'Docs', icon: BookOpen },
]

// The active tab is anchored in the URL hash (#tokens) so a reload lands on
// the same view and back/forward moves between tabs — a full router would be
// overkill for six flat tabs.
function syncTabFromHash() {
  const h = window.location.hash.replace(/^#\/?/, '')
  if (tabs.some((t) => t.id === h)) tab.value = h
}
syncTabFromHash()
watch(tab, (t) => {
  if (window.location.hash.replace(/^#\/?/, '') !== t) window.location.hash = t
})
onMounted(() => window.addEventListener('hashchange', syncTabFromHash))
onUnmounted(() => window.removeEventListener('hashchange', syncTabFromHash))

async function tryLogin() {
  loginError.value = ''
  try {
    // Login sets the httpOnly session cookie; the token never touches JS.
    await api.login({ password: loginPassword.value })
    authed.value = true
    loginPassword.value = ''
  } catch (e) {
    loginError.value = isUnauthenticated(e) ? 'Invalid password' : `Cannot reach the bot: ${errMsg(e)}`
  }
}

async function logout() {
  try {
    await api.logout({})
  } catch {
    // The cookie may already be dead; drop to the login screen regardless.
  }
  authed.value = false
  tab.value = 'status'
}

onMounted(async () => {
  // An existing session cookie (7d validity) survives page reloads.
  try {
    await api.getStatus({})
    authed.value = true
  } catch {
    // no valid session; show the login form
  }
})
</script>

<template>
  <nav class="mb-6 border-b">
    <div class="flex h-14 items-center gap-6 px-6">
      <span class="flex items-center gap-2 font-semibold">
        <Bell class="size-4" />tocsin
      </span>
      <div v-if="authed" class="flex flex-1 items-center gap-1">
        <button
          v-for="t in tabs"
          :key="t.id"
          class="inline-flex cursor-pointer items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
          :class="tab === t.id ? 'bg-secondary text-foreground' : 'text-muted-foreground hover:text-foreground'"
          @click="tab = t.id"
        >
          <component :is="t.icon" class="size-4" />{{ t.label }}
        </button>
      </div>
      <Button v-if="authed" variant="outline" size="sm" @click="logout">
        <LogOut />Logout
      </Button>
    </div>
  </nav>

  <div class="px-6">
    <div v-if="!authed" class="mx-auto mt-16 w-full max-w-md">
      <Card>
        <template #header><Lock />Admin login</template>
        <form class="space-y-3" @submit.prevent="tryLogin">
          <Input v-model="loginPassword" type="password" placeholder="Admin password" autofocus />
          <Alert v-if="loginError" variant="destructive">{{ loginError }}</Alert>
          <Button class="w-full" type="submit">Sign in</Button>
        </form>
      </Card>
    </div>

    <template v-else>
      <StatusPanel v-if="tab === 'status'" />
      <ChannelsPanel v-if="tab === 'channels'" />
      <TokensPanel v-if="tab === 'tokens'" />
      <HistoryPanel v-if="tab === 'history'" />
      <SettingsPanel v-if="tab === 'settings'" />
      <DocsPanel v-if="tab === 'docs'" />
    </template>
  </div>

  <footer class="mt-10 border-t px-6 py-4 text-center text-xs text-muted-foreground">
    <a
      href="https://github.com/thomas-maurice/tocsin"
      target="_blank"
      rel="noopener"
      class="inline-flex items-center gap-1.5 transition-colors hover:text-foreground"
    >
      <i class="fa-brands fa-github"></i>thomas-maurice/tocsin
    </a>
  </footer>
</template>
