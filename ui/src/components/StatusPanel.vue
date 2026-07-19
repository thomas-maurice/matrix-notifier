<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { Bot, Gauge, Hash, ShieldCheck, TriangleAlert, Check, X, Lock, LockOpen } from '@lucide/vue'
import { api, errMsg } from '../api'
import { notifyError } from '../toast'
import Card from './ui/Card.vue'
import Badge from './ui/Badge.vue'
import RoomRef from './RoomRef.vue'
import type { Channel, GetStatusResponse } from '../gen/notifier/v1/admin_pb'

const status = ref<GetStatusResponse | null>(null)
const channels = ref<Channel[]>([])
let timer: ReturnType<typeof setInterval> | undefined
// This panel polls every 10s; only toast when the error state changes so a
// persistent outage doesn't spam a toast every tick.
let lastError = ''

const syncHealthy = computed(() => {
  const ts = status.value?.lastSync
  if (!ts) return false
  return Date.now() - timestampDate(ts).getTime() < 90_000
})

function fmtUptime(seconds: bigint): string {
  const s = Number(seconds)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return [d && `${d}d`, (h || d) && `${h}h`, `${m}m`].filter(Boolean).join(' ')
}

async function refresh() {
  try {
    const [st, ch] = await Promise.all([api.getStatus({}), api.listChannels({})])
    status.value = st
    channels.value = ch.channels
    lastError = ''
  } catch (e) {
    const msg = errMsg(e)
    if (msg !== lastError) {
      lastError = msg
      notifyError(msg)
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
  <div v-if="status" class="grid grid-cols-1 gap-4 md:grid-cols-2">
    <Card flush>
      <template #header><Bot />Bot</template>
      <table class="data-table">
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
              <Badge :variant="status.verified ? 'success' : 'destructive'">
                <ShieldCheck v-if="status.verified" />
                <TriangleAlert v-else />
                {{ status.verified ? 'cross-signed' : 'not verified' }}
              </Badge>
            </td>
          </tr>
          <tr>
            <th>Sync</th>
            <td>
              <Badge :variant="syncHealthy ? 'success' : 'destructive'">
                {{ syncHealthy ? 'healthy' : 'stalled' }}
              </Badge>
            </td>
          </tr>
        </tbody>
      </table>
    </Card>
    <Card flush>
      <template #header><Gauge />Runtime</template>
      <table class="data-table">
        <tbody>
          <tr>
            <th>Uptime</th>
            <td>{{ fmtUptime(status.uptimeSeconds) }}</td>
          </tr>
          <tr>
            <th>Delivered since start</th>
            <td>{{ status.deliveredSinceStart }}</td>
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
    </Card>
    <Card flush class="md:col-span-2">
      <template #header><Hash />Channel health</template>
      <table class="data-table hoverable">
        <thead>
          <tr><th>Channel</th><th>Room</th><th>Joined</th><th>Encrypted</th></tr>
        </thead>
        <tbody>
          <tr v-for="ch in channels" :key="ch.name">
            <td>{{ ch.name }}</td>
            <td><RoomRef :room-id="ch.roomId" :alias="ch.alias" /></td>
            <td>
              <Check v-if="ch.joined" class="size-4 text-green-400" />
              <X v-else class="size-4 text-red-400" />
            </td>
            <td>
              <Lock v-if="ch.encrypted" class="size-4 text-green-400" />
              <LockOpen v-else class="size-4 text-red-400" />
            </td>
          </tr>
          <tr v-if="!channels.length">
            <td colspan="4" class="py-4 text-center text-muted-foreground">No channels configured</td>
          </tr>
        </tbody>
      </table>
    </Card>
  </div>
</template>
