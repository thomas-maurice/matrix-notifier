<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api.js'
import { notifyError, notifySuccess } from '../toast.js'

const channels = ref([])
const rooms = ref([])
const newName = ref('')
const newRoomId = ref('')
const newChart = ref(false)

const unmappedRooms = computed(() => rooms.value.filter((r) => !r.channel))

async function refresh() {
  try {
    const [ch, rm] = await Promise.all([api.listChannels(), api.listRooms()])
    channels.value = ch.channels || []
    rooms.value = rm.rooms || []
  } catch (e) {
    notifyError(e.message)
  }
}

function suggestedName(room) {
  return (room.name || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
}

// One click adds the room as a channel under its suggested name; if that
// isn't possible (no name / name taken), fall back to pre-filling the form.
async function addRoom(room) {
  const name = suggestedName(room)
  if (!name) {
    newRoomId.value = room.roomId
    notifyError('Room has no name — pick a channel name and hit Create.')
    return
  }
  try {
    await api.createChannel(name, room.roomId, false)
    notifySuccess(`Channel "${name}" created for ${room.name}`)
    await refresh()
  } catch (e) {
    newRoomId.value = room.roomId
    newName.value = name
    notifyError(`Could not auto-create "${name}" (${e.message}) — adjust and hit Create.`)
  }
}

async function create() {
  try {
    await api.createChannel(newName.value.trim(), newRoomId.value.trim(), newChart.value)
    notifySuccess(`Channel "${newName.value.trim()}" created`)
    newName.value = ''
    newRoomId.value = ''
    newChart.value = false
    await refresh()
  } catch (e) {
    notifyError(e.message)
  }
}

async function remove(name) {
  if (!confirm(`Delete channel "${name}"?`)) return
  try {
    await api.deleteChannel(name)
    notifySuccess(`Channel "${name}" deleted`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
  }
}

async function toggleChart(ch) {
  try {
    await api.updateChannel(ch.name, !ch.chart)
    notifySuccess(`Charts ${ch.chart ? 'disabled' : 'enabled'} for "${ch.name}"`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
    await refresh() // snap the switch back to reality
  }
}

async function leave(room) {
  const label = room.name || room.roomId
  if (!confirm(`Leave room "${label}"? Any channel mapped to it (and its tokens) will be deleted.`)) return
  try {
    await api.leaveRoom(room.roomId)
    notifySuccess(`Left ${label}`)
    await refresh()
  } catch (e) {
    notifyError(e.message)
  }
}

async function sendTest(name) {
  try {
    await api.sendTest(name)
    notifySuccess(`Test notification sent to "${name}"`)
  } catch (e) {
    notifyError(e.message)
  }
}

onMounted(refresh)
</script>

<template>

  <div class="card mb-4">
    <div class="card-header"><i class="fa-solid fa-plus me-2"></i>New channel</div>
    <div class="card-body">
      <form class="row g-2 align-items-center" @submit.prevent="create">
        <div class="col-md-3">
          <input v-model="newName" class="form-control" placeholder="name (e.g. infra-alerts)" required />
        </div>
        <div class="col-md-5">
          <input v-model="newRoomId" class="form-control" placeholder="!roomid:example.org or #alias:example.org" required />
        </div>
        <div class="col-md-2 form-check form-switch ms-2">
          <input id="chartSwitch" v-model="newChart" class="form-check-input" type="checkbox" />
          <label class="form-check-label" for="chartSwitch" title="Attach a Prometheus chart to alertmanager notifications">
            <i class="fa-solid fa-chart-line"></i> chart
          </label>
        </div>
        <div class="col-md-2">
          <button class="btn btn-primary w-100" type="submit">Create</button>
        </div>
      </form>
      <div class="form-text mt-2">
        Create an <strong>encrypted</strong> room, invite the bot, then map it here (room ID or alias). The bot joins on its own.
      </div>
      <div v-if="unmappedRooms.length" class="mt-2">
        <span class="form-text me-2">Joined rooms without a channel:</span>
        <div v-for="room in unmappedRooms" :key="room.roomId" class="btn-group me-2 mb-1">
          <button
            type="button"
            class="btn btn-sm btn-outline-success"
            :title="`Create channel '${suggestedName(room) || '?'}' for ${room.roomId}`"
            @click="addRoom(room)"
          >
            <i class="fa-solid fa-plus me-1"></i>
            <i :class="room.encrypted ? 'fa-solid fa-lock me-1' : 'fa-solid fa-lock-open text-danger me-1'"></i>
            {{ room.name || room.roomId }}
          </button>
          <button
            type="button"
            class="btn btn-sm btn-outline-danger"
            :title="`Leave ${room.roomId}`"
            @click="leave(room)"
          >
            <i class="fa-solid fa-door-open"></i>
          </button>
        </div>
      </div>
    </div>
  </div>

  <div class="card">
    <div class="card-header"><i class="fa-solid fa-hashtag me-2"></i>Channels</div>
    <div class="card-body p-0">
      <table class="table mb-0 align-middle">
        <thead>
          <tr><th class="ps-3">Name</th><th>Room</th><th>Joined</th><th>Encrypted</th><th>Chart</th><th class="text-end pe-3">Actions</th></tr>
        </thead>
        <tbody>
          <tr v-for="ch in channels" :key="ch.name">
            <td class="ps-3">{{ ch.name }}</td>
            <td><code>{{ ch.roomId }}</code></td>
            <td><i :class="ch.joined ? 'fa-solid fa-check text-success' : 'fa-solid fa-xmark text-danger'"></i></td>
            <td><i :class="ch.encrypted ? 'fa-solid fa-lock text-success' : 'fa-solid fa-lock-open text-danger'"></i></td>
            <td>
              <div class="form-check form-switch mb-0" :title="ch.chart ? 'Charts on — click to disable' : 'Charts off — click to enable'">
                <input
                  class="form-check-input"
                  type="checkbox"
                  :checked="ch.chart"
                  @change="toggleChart(ch)"
                />
              </div>
            </td>
            <td class="text-end pe-3">
              <button class="btn btn-sm btn-outline-info me-2" title="Send test notification" @click="sendTest(ch.name)">
                <i class="fa-solid fa-paper-plane"></i>
              </button>
              <button class="btn btn-sm btn-outline-danger" title="Delete" @click="remove(ch.name)">
                <i class="fa-solid fa-trash"></i>
              </button>
            </td>
          </tr>
          <tr v-if="!channels.length">
            <td colspan="6" class="text-center text-secondary py-3">No channels yet</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
