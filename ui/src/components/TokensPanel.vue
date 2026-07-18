<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api.js'
import { notifyError, notifySuccess } from '../toast.js'

const tokens = ref([])
const channels = ref([])
const minted = ref(null)
const newName = ref('')
const newKind = ref('any')
const newChannel = ref('')
const newPrefix = ref('')

async function refresh() {
  try {
    const [t, c] = await Promise.all([api.listTokens(), api.listChannels()])
    tokens.value = t.tokens || []
    channels.value = c.channels || []
    if (!newChannel.value && channels.value.length) newChannel.value = channels.value[0].name
  } catch (e) {
    notifyError(e.message)
  }
}

async function create() {
  minted.value = null
  try {
    const resp = await api.createToken(newName.value.trim(), newKind.value, newChannel.value, newPrefix.value.trim())
    minted.value = resp
    newName.value = ''
    newPrefix.value = ''
    notifySuccess(`Token "${resp.token.name}" created`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
  }
}

async function editPrefix(tok) {
  const prefix = prompt(`Notification prefix for "${tok.name}" (emoji or short text, empty to clear):`, tok.prefix || '')
  if (prefix === null) return
  try {
    // Send the current channel so editing the prefix never reroutes the token.
    await api.updateToken(tok.name, prefix.trim(), tok.channel)
    notifySuccess(`Prefix updated for "${tok.name}"`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
  }
}

async function changeChannel(tok, event) {
  const channel = event.target.value
  try {
    await api.updateToken(tok.name, tok.prefix || '', channel)
    notifySuccess(`Token "${tok.name}" now routes to "${channel}"`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
    await refresh()
  }
}

async function remove(name) {
  if (!confirm(`Delete token "${name}"? Producers using it will get 401s.`)) return
  try {
    await api.deleteToken(name)
    notifySuccess(`Token "${name}" deleted`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
  }
}

async function test(tok) {
  try {
    await api.testToken(tok.name)
    notifySuccess(`Test notification sent via "${tok.name}" to ${tok.channel}`)
  } catch (e) {
    notifyError(e.message)
  }
}

function copyMinted() {
  navigator.clipboard?.writeText(minted.value.plaintext)
}

function fmtDate(ts) {
  return ts ? new Date(ts).toLocaleString() : '—'
}

onMounted(refresh)
</script>

<template>

  <div v-if="minted" class="alert alert-warning">
    <div class="fw-bold mb-1"><i class="fa-solid fa-triangle-exclamation me-1"></i>
      Copy this token now — it is shown exactly once:</div>
    <code class="user-select-all">{{ minted.plaintext }}</code>
    <button class="btn btn-sm btn-outline-dark ms-2" @click="copyMinted"><i class="fa-solid fa-copy"></i></button>
  </div>

  <div class="card mb-4">
    <div class="card-header"><i class="fa-solid fa-plus me-2"></i>New token</div>
    <div class="card-body">
      <form class="row g-2" @submit.prevent="create">
        <div class="col-md-3">
          <input v-model="newName" class="form-control" placeholder="name (e.g. sonarr)" required />
        </div>
        <div class="col-md-3">
          <select v-model="newKind" class="form-select">
            <option value="any">any endpoint</option>
            <option value="gotify">gotify only</option>
            <option value="alertmanager">alertmanager only</option>
            <option value="gitea">gitea/forgejo only</option>
            <option value="slack">slack only</option>
          </select>
        </div>
        <div class="col-md-2">
          <select v-model="newChannel" class="form-select" required>
            <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
          </select>
        </div>
        <div class="col-md-2">
          <input v-model="newPrefix" class="form-control" placeholder="prefix (optional)" title="Prepended to notification titles" />
        </div>
        <div class="col-md-2">
          <button class="btn btn-primary w-100" type="submit" :disabled="!channels.length">Create</button>
        </div>
      </form>
      <div v-if="!channels.length" class="form-text mt-2 text-warning">Create a channel first.</div>
    </div>
  </div>

  <div class="card">
    <div class="card-header"><i class="fa-solid fa-key me-2"></i>Tokens</div>
    <div class="card-body p-0">
      <table class="table mb-0 align-middle">
        <thead>
          <tr><th class="ps-3">Name</th><th>Kind</th><th>Channel</th><th>Prefix</th><th>Created</th><th>Last used</th><th class="text-end pe-3"></th></tr>
        </thead>
        <tbody>
          <tr v-for="tok in tokens" :key="tok.name">
            <td class="ps-3">{{ tok.name }}</td>
            <td><span class="badge text-bg-secondary">{{ tok.kind }}</span></td>
            <td>
              <select class="form-select form-select-sm" :value="tok.channel" @change="changeChannel(tok, $event)" title="Route this token to another room">
                <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
              </select>
            </td>
            <td>
              <button class="btn btn-sm btn-outline-secondary" title="Edit prefix" @click="editPrefix(tok)">
                {{ tok.prefix || '—' }} <i class="fa-solid fa-pen ms-1 small"></i>
              </button>
            </td>
            <td class="text-secondary">{{ fmtDate(tok.createdAt) }}</td>
            <td class="text-secondary">{{ fmtDate(tok.lastUsedAt) }}</td>
            <td class="text-end pe-3">
              <button class="btn btn-sm btn-outline-info me-2" title="Send a test notification through this token" @click="test(tok)">
                <i class="fa-solid fa-paper-plane"></i>
              </button>
              <button class="btn btn-sm btn-outline-danger" title="Delete" @click="remove(tok.name)">
                <i class="fa-solid fa-trash"></i>
              </button>
            </td>
          </tr>
          <tr v-if="!tokens.length">
            <td colspan="7" class="text-center text-secondary py-3">No tokens yet</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
