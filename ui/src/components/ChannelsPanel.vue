<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Plus, Hash, Check, X, Lock, LockOpen, Send, Trash2, DoorOpen, ChartLine, MessageSquare } from '@lucide/vue'
import { api, errMsg } from '../api'
import { notifyError, notifySuccess } from '../toast'
import Card from './ui/Card.vue'
import Button from './ui/Button.vue'
import Input from './ui/Input.vue'
import RoomRef from './RoomRef.vue'
import type { Channel, Room } from '../gen/notifier/v1/admin_pb'

const channels = ref<Channel[]>([])
const rooms = ref<Room[]>([])
const newName = ref('')
const newRoomId = ref('')
const newChart = ref(false)

// DMs (nameless two-member rooms, e.g. the room Element creates to verify
// the bot) are listed apart: they are conversations, not notification
// targets, and auto-naming a channel from them can never work.
const unmappedRooms = computed(() => rooms.value.filter((r) => !r.channel && !r.dmWith))
const dmRooms = computed(() => rooms.value.filter((r) => !r.channel && r.dmWith))

async function refresh() {
  try {
    const [ch, rm] = await Promise.all([api.listChannels({}), api.listRooms({})])
    channels.value = ch.channels
    rooms.value = rm.rooms
  } catch (e) {
    notifyError(errMsg(e))
  }
}

function suggestedName(room: Room): string {
  return (room.name || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
}

// One click adds the room as a channel under its suggested name; if that
// isn't possible (no name / name taken), fall back to pre-filling the form.
async function addRoom(room: Room) {
  const name = suggestedName(room)
  if (!name) {
    newRoomId.value = room.roomId
    notifyError('Room has no name — pick a channel name and hit Create.')
    return
  }
  try {
    await api.createChannel({ name, roomId: room.roomId, chart: false })
    notifySuccess(`Channel "${name}" created for ${room.name}`)
    await refresh()
  } catch (e) {
    newRoomId.value = room.roomId
    newName.value = name
    notifyError(`Could not auto-create "${name}" (${errMsg(e)}) — adjust and hit Create.`)
  }
}

async function create() {
  try {
    await api.createChannel({ name: newName.value.trim(), roomId: newRoomId.value.trim(), chart: newChart.value })
    notifySuccess(`Channel "${newName.value.trim()}" created`)
    newName.value = ''
    newRoomId.value = ''
    newChart.value = false
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function remove(name: string) {
  if (!confirm(`Delete channel "${name}"?`)) return
  try {
    await api.deleteChannel({ name })
    notifySuccess(`Channel "${name}" deleted`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function toggleChart(ch: Channel) {
  try {
    await api.updateChannel({ name: ch.name, chart: !ch.chart })
    notifySuccess(`Charts ${ch.chart ? 'disabled' : 'enabled'} for "${ch.name}"`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
    await refresh() // snap the switch back to reality
  }
}

async function leave(room: Room) {
  const label = room.name || room.roomId
  if (!confirm(`Leave room "${label}"? Any channel mapped to it (and its tokens) will be deleted.`)) return
  try {
    await api.leaveRoom({ roomId: room.roomId })
    notifySuccess(`Left ${label}`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function sendTest(name: string) {
  try {
    await api.sendTestNotification({ channel: name })
    notifySuccess(`Test notification sent to "${name}"`)
  } catch (e) {
    notifyError(errMsg(e))
  }
}

onMounted(refresh)
</script>

<template>
  <Card class="mb-4">
    <template #header><Plus />New channel</template>
    <form class="grid grid-cols-1 items-center gap-2 md:grid-cols-12" @submit.prevent="create">
      <Input v-model="newName" class="md:col-span-3" placeholder="name (e.g. infra-alerts)" required />
      <Input
        v-model="newRoomId"
        class="md:col-span-5"
        placeholder="!roomid:example.org or #alias:example.org"
        required
      />
      <label
        class="flex items-center gap-2 text-sm md:col-span-2"
        title="Attach a Prometheus chart to alertmanager notifications"
      >
        <input v-model="newChart" type="checkbox" class="switch" />
        <ChartLine class="size-4" /> chart
      </label>
      <Button class="md:col-span-2" type="submit">Create</Button>
    </form>
    <p class="mt-3 text-xs text-muted-foreground">
      Create an <strong>encrypted, named</strong> room, invite the bot, then map it here (room ID or alias). The bot
      joins on its own. Nameless two-member rooms are treated as direct messages and not offered below.
    </p>
    <div v-if="unmappedRooms.length" class="mt-2 flex flex-wrap items-center gap-2">
      <span class="text-xs text-muted-foreground">Joined rooms without a channel:</span>
      <div v-for="room in unmappedRooms" :key="room.roomId" class="inline-flex">
        <Button
          variant="outline"
          size="sm"
          class="rounded-r-none border-r-0 text-green-400"
          :title="`Create channel '${suggestedName(room) || '?'}' for ${room.roomId}`"
          @click="addRoom(room)"
        >
          <Plus />
          <Lock v-if="room.encrypted" />
          <LockOpen v-else class="text-red-400" />
          {{ room.name || room.roomId }}
        </Button>
        <Button
          variant="outline"
          size="sm"
          class="rounded-l-none text-red-400"
          :title="`Leave ${room.roomId}`"
          @click="leave(room)"
        >
          <DoorOpen />
        </Button>
      </div>
    </div>
  </Card>

  <Card flush>
    <template #header><Hash />Channels</template>
    <table class="data-table hoverable">
      <thead>
        <tr><th>Name</th><th>Room</th><th>Joined</th><th>Encrypted</th><th>Chart</th><th class="text-right">Actions</th></tr>
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
          <td>
            <input
              type="checkbox"
              class="switch"
              :checked="ch.chart"
              :title="ch.chart ? 'Charts on — click to disable' : 'Charts off — click to enable'"
              @change="toggleChart(ch)"
            />
          </td>
          <td class="text-right">
            <div class="inline-flex gap-2">
              <Button variant="outline" size="icon-sm" title="Send test notification" @click="sendTest(ch.name)">
                <Send />
              </Button>
              <Button variant="outline" size="icon-sm" class="text-red-400" title="Delete" @click="remove(ch.name)">
                <Trash2 />
              </Button>
            </div>
          </td>
        </tr>
        <tr v-if="!channels.length">
          <td colspan="6" class="py-4 text-center text-muted-foreground">No channels yet</td>
        </tr>
      </tbody>
    </table>
  </Card>

  <Card v-if="dmRooms.length" flush class="mt-4">
    <template #header><MessageSquare />Direct messages</template>
    <table class="data-table hoverable">
      <thead>
        <tr><th>User</th><th>Room</th><th>Encrypted</th><th class="text-right">Actions</th></tr>
      </thead>
      <tbody>
        <tr v-for="room in dmRooms" :key="room.roomId">
          <td>{{ room.dmWith }}</td>
          <td><RoomRef :room-id="room.roomId" /></td>
          <td>
            <Lock v-if="room.encrypted" class="size-4 text-green-400" />
            <LockOpen v-else class="size-4 text-red-400" />
          </td>
          <td class="text-right">
            <Button variant="outline" size="sm" class="text-red-400" :title="`Leave ${room.roomId}`" @click="leave(room)">
              <DoorOpen />Leave
            </Button>
          </td>
        </tr>
      </tbody>
    </table>
    <p class="px-3 py-2 text-xs text-muted-foreground">
      Conversations with the bot (e.g. verification), not notification targets — use
      <code>!notify</code> commands here.
    </p>
  </Card>
</template>
