<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { History, RefreshCw, RotateCw, TriangleAlert, Check } from '@lucide/vue'
import { api, errMsg } from '../api'
import { notifyError, notifySuccess } from '../toast'
import Card from './ui/Card.vue'
import Button from './ui/Button.vue'
import Badge from './ui/Badge.vue'
import type { Channel, Delivery } from '../gen/notifier/v1/admin_pb'

const deliveries = ref<Delivery[]>([])
const channels = ref<Channel[]>([])
const channelFilter = ref('')
const expanded = ref<bigint | null>(null)

async function refresh() {
  try {
    const [d, c] = await Promise.all([
      api.listDeliveries({ channel: channelFilter.value }),
      api.listChannels({}),
    ])
    deliveries.value = d.deliveries
    channels.value = c.channels
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function retry(d: Delivery) {
  try {
    await api.retryDelivery({ id: d.id })
    notifySuccess(`Delivery #${d.id} queued again`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

function toggle(d: Delivery) {
  expanded.value = expanded.value === d.id ? null : d.id
}

function statusVariant(status: string): 'success' | 'destructive' | 'warning' {
  switch (status) {
    case 'delivered':
      return 'success'
    case 'failed':
      return 'destructive'
    default:
      return 'warning'
  }
}

function fmtDate(ts?: Timestamp): string {
  return ts ? timestampDate(ts).toLocaleString() : '—'
}

// The queue drains asynchronously; keep the view live while it is open.
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  refresh()
  timer = setInterval(refresh, 10_000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <Card flush>
    <template #header>
      <History /><span class="flex-1">Delivery history</span>
      <select v-model="channelFilter" class="select select-sm w-auto" title="Filter by channel" @change="refresh">
        <option value="">all channels</option>
        <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
      </select>
      <Button variant="outline" size="icon-sm" title="Refresh" @click="refresh">
        <RefreshCw />
      </Button>
    </template>
    <table class="data-table hoverable">
      <thead>
        <tr>
          <th>Time</th>
          <th>Channel</th>
          <th>Kind</th>
          <th>Title</th>
          <th>Status</th>
          <th>Attempts</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="d in deliveries" :key="d.id">
          <tr class="cursor-pointer" @click="toggle(d)">
            <td class="whitespace-nowrap text-muted-foreground">{{ fmtDate(d.createdAt) }}</td>
            <td>{{ d.channel }}</td>
            <td><Badge variant="secondary">{{ d.kind }}</Badge></td>
            <td class="max-w-[220px] truncate">{{ d.title || d.body }}</td>
            <td><Badge :variant="statusVariant(d.status)">{{ d.status }}</Badge></td>
            <td class="text-muted-foreground">{{ d.attempts }}</td>
            <td class="text-right">
              <Button
                v-if="d.status === 'failed'"
                variant="outline"
                size="icon-sm"
                class="text-amber-400"
                title="Queue this delivery again"
                @click.stop="retry(d)"
              >
                <RotateCw />
              </Button>
            </td>
          </tr>
          <tr v-if="expanded === d.id">
            <td colspan="7" class="text-xs">
              <div class="whitespace-pre-wrap text-muted-foreground">{{ d.body }}</div>
              <div v-if="d.lastError" class="mt-1 flex items-center gap-1 text-red-400">
                <TriangleAlert class="size-3.5" />{{ d.lastError }}
              </div>
              <div v-if="d.deliveredAt" class="mt-1 flex items-center gap-1 text-green-400">
                <Check class="size-3.5" />delivered {{ fmtDate(d.deliveredAt) }}
              </div>
            </td>
          </tr>
        </template>
        <tr v-if="!deliveries.length">
          <td colspan="7" class="py-4 text-center text-muted-foreground">No deliveries yet</td>
        </tr>
      </tbody>
    </table>
  </Card>
  <p class="mt-2 text-xs text-muted-foreground">
    Ingest endpoints answer <code>200</code> as soon as a notification is queued; delivery is retried
    with backoff for up to 24 hours, then marked failed (retryable here by hand).
  </p>
</template>
