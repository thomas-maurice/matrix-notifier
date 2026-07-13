<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api.js'

const tokens = ref([])
const channels = ref([])
const error = ref('')
const minted = ref(null)
const newName = ref('')
const newKind = ref('any')
const newChannel = ref('')

async function refresh() {
  try {
    const [t, c] = await Promise.all([api.listTokens(), api.listChannels()])
    tokens.value = t.tokens || []
    channels.value = c.channels || []
    if (!newChannel.value && channels.value.length) newChannel.value = channels.value[0].name
    error.value = ''
  } catch (e) {
    error.value = e.message
  }
}

async function create() {
  error.value = ''
  minted.value = null
  try {
    const resp = await api.createToken(newName.value.trim(), newKind.value, newChannel.value)
    minted.value = resp
    newName.value = ''
    await refresh()
  } catch (e) {
    error.value = e.message
  }
}

async function remove(name) {
  error.value = ''
  if (!confirm(`Delete token "${name}"? Producers using it will get 401s.`)) return
  try {
    await api.deleteToken(name)
    await refresh()
  } catch (e) {
    error.value = e.message
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
  <div v-if="error" class="alert alert-danger">{{ error }}</div>

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
        <div class="col-md-4">
          <input v-model="newName" class="form-control" placeholder="name (e.g. prometheus)" required />
        </div>
        <div class="col-md-3">
          <select v-model="newKind" class="form-select">
            <option value="any">any endpoint</option>
            <option value="gotify">gotify only</option>
            <option value="alertmanager">alertmanager only</option>
          </select>
        </div>
        <div class="col-md-3">
          <select v-model="newChannel" class="form-select" required>
            <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
          </select>
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
          <tr><th class="ps-3">Name</th><th>Kind</th><th>Channel</th><th>Created</th><th>Last used</th><th class="text-end pe-3"></th></tr>
        </thead>
        <tbody>
          <tr v-for="tok in tokens" :key="tok.name">
            <td class="ps-3">{{ tok.name }}</td>
            <td><span class="badge text-bg-secondary">{{ tok.kind }}</span></td>
            <td>{{ tok.channel }}</td>
            <td class="text-secondary">{{ fmtDate(tok.createdAt) }}</td>
            <td class="text-secondary">{{ fmtDate(tok.lastUsedAt) }}</td>
            <td class="text-end pe-3">
              <button class="btn btn-sm btn-outline-danger" @click="remove(tok.name)">
                <i class="fa-solid fa-trash"></i>
              </button>
            </td>
          </tr>
          <tr v-if="!tokens.length">
            <td colspan="6" class="text-center text-secondary py-3">No tokens yet</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
