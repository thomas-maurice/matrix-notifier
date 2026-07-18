<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { api } from '../api.js'
import { notifyError } from '../toast.js'
import RoomRef from './RoomRef.vue'

const status = ref(null)
const channels = ref([])
let timer
// This panel polls every 10s; only toast when the error state changes so a
// persistent outage doesn't spam a toast every tick.
let lastError = ''

const syncHealthy = computed(() => {
  if (!status.value?.lastSync) return false
  return Date.now() - new Date(status.value.lastSync).getTime() < 90_000
})

function fmtUptime(seconds) {
  const s = Number(seconds || 0)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return [d && `${d}d`, (h || d) && `${h}h`, `${m}m`].filter(Boolean).join(' ')
}

async function refresh() {
  try {
    const [st, ch] = await Promise.all([api.getStatus(), api.listChannels()])
    status.value = st
    channels.value = ch.channels || []
    lastError = ''
  } catch (e) {
    if (e.message !== lastError) {
      lastError = e.message
      notifyError(e.message)
    }
  }
}

onMounted(() => {
  refresh()
  timer = setInterval(refresh, 10_000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="status" class="row g-4">
    <div class="col-md-6">
      <div class="card h-100">
        <div class="card-header"><i class="fa-solid fa-robot me-2"></i>Bot</div>
        <div class="card-body">
          <table class="table table-sm mb-0">
            <tbody>
              <tr>
                <th>User</th>
                <td><code>{{ status.userId }}</code></td>
              </tr>
              <tr>
                <th>Device</th>
                <td><code>{{ status.deviceId }}</code></td>
              </tr>
              <tr>
                <th>Verified</th>
                <td>
                  <span :class="status.verified ? 'badge text-bg-success' : 'badge text-bg-danger'">
                    <i :class="status.verified ? 'fa-solid fa-shield-halved' : 'fa-solid fa-triangle-exclamation'" class="me-1"></i>
                    {{ status.verified ? 'cross-signed' : 'not verified' }}
                  </span>
                </td>
              </tr>
              <tr>
                <th>Sync</th>
                <td>
                  <span :class="syncHealthy ? 'badge text-bg-success' : 'badge text-bg-danger'">
                    {{ syncHealthy ? 'healthy' : 'stalled' }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
    <div class="col-md-6">
      <div class="card h-100">
        <div class="card-header"><i class="fa-solid fa-gauge me-2"></i>Runtime</div>
        <div class="card-body">
          <table class="table table-sm mb-0">
            <tbody>
              <tr>
                <th>Uptime</th>
                <td>{{ fmtUptime(status.uptimeSeconds) }}</td>
              </tr>
              <tr>
                <th>Delivered since start</th>
                <td>{{ status.deliveredSinceStart || 0 }}</td>
              </tr>
              <tr>
                <th>Database</th>
                <td><code>{{ status.databaseType }}</code></td>
              </tr>
              <tr>
                <th>Version</th>
                <td><code>{{ status.version }}</code></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
    <div class="col-12">
      <div class="card">
        <div class="card-header"><i class="fa-solid fa-hashtag me-2"></i>Channel health</div>
        <div class="card-body p-0">
          <table class="table table-sm mb-0 align-middle">
            <thead>
              <tr><th class="ps-3">Channel</th><th>Room</th><th>Joined</th><th>Encrypted</th></tr>
            </thead>
            <tbody>
              <tr v-for="ch in channels" :key="ch.name">
                <td class="ps-3">{{ ch.name }}</td>
                <td><RoomRef :room-id="ch.roomId" :alias="ch.alias" /></td>
                <td><i :class="ch.joined ? 'fa-solid fa-check text-success' : 'fa-solid fa-xmark text-danger'"></i></td>
                <td><i :class="ch.encrypted ? 'fa-solid fa-lock text-success' : 'fa-solid fa-lock-open text-danger'"></i></td>
              </tr>
              <tr v-if="!channels.length">
                <td colspan="4" class="text-center text-secondary py-3">No channels configured</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>
</template>
