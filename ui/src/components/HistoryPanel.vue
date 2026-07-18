<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { api, errMsg } from '../api'
import { notifyError, notifySuccess } from '../toast'
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

function statusBadge(status: string): string {
  switch (status) {
    case 'delivered':
      return 'text-bg-success'
    case 'failed':
      return 'text-bg-danger'
    default:
      return 'text-bg-warning'
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
  <div class="card">
    <div class="card-header d-flex align-items-center">
      <span><i class="fa-solid fa-clock-rotate-left me-2"></i>Delivery history</span>
      <select
        v-model="channelFilter"
        class="form-select form-select-sm ms-auto"
        style="width: auto"
        title="Filter by channel"
        @change="refresh"
      >
        <option value="">all channels</option>
        <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
      </select>
      <button class="btn btn-sm btn-outline-secondary ms-2" title="Refresh" @click="refresh">
        <i class="fa-solid fa-rotate"></i>
      </button>
    </div>
    <div class="card-body p-0">
      <table class="table mb-0 align-middle">
        <thead>
          <tr>
            <th class="ps-3">Time</th>
            <th>Channel</th>
            <th>Kind</th>
            <th>Title</th>
            <th>Status</th>
            <th>Attempts</th>
            <th class="text-end pe-3"></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="d in deliveries" :key="d.id">
            <tr role="button" @click="toggle(d)">
              <td class="ps-3 text-secondary">{{ fmtDate(d.createdAt) }}</td>
              <td>{{ d.channel }}</td>
              <td><span class="badge text-bg-secondary">{{ d.kind }}</span></td>
              <td class="text-truncate" style="max-width: 220px">{{ d.title || d.body }}</td>
              <td><span class="badge" :class="statusBadge(d.status)">{{ d.status }}</span></td>
              <td class="text-secondary">{{ d.attempts }}</td>
              <td class="text-end pe-3">
                <button
                  v-if="d.status === 'failed'"
                  class="btn btn-sm btn-outline-warning"
                  title="Queue this delivery again"
                  @click.stop="retry(d)"
                >
                  <i class="fa-solid fa-rotate-right"></i>
                </button>
              </td>
            </tr>
            <tr v-if="expanded === d.id">
              <td colspan="7" class="ps-3 small">
                <div class="text-secondary" style="white-space: pre-wrap">{{ d.body }}</div>
                <div v-if="d.lastError" class="text-danger mt-1">
                  <i class="fa-solid fa-triangle-exclamation me-1"></i>{{ d.lastError }}
                </div>
                <div v-if="d.deliveredAt" class="text-success mt-1">
                  <i class="fa-solid fa-check me-1"></i>delivered {{ fmtDate(d.deliveredAt) }}
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="!deliveries.length">
            <td colspan="7" class="text-center text-secondary py-3">No deliveries yet</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
  <p class="text-secondary small mt-2 mb-0">
    Ingest endpoints answer <code>200</code> as soon as a notification is queued; delivery is retried
    with backoff for up to 24 hours, then marked failed (retryable here by hand).
  </p>
</template>
