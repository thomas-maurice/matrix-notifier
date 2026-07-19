<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { timestampDate, timestampFromDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { Plus, KeyRound, TriangleAlert, Copy, Pencil, Send, Trash2 } from '@lucide/vue'
import { api, errMsg } from '../api'
import { notifyError, notifySuccess } from '../toast'
import Card from './ui/Card.vue'
import Button from './ui/Button.vue'
import Input from './ui/Input.vue'
import Badge from './ui/Badge.vue'
import Alert from './ui/Alert.vue'
import Dialog from './ui/Dialog.vue'
import type { Channel, CreateTokenResponse, Token } from '../gen/notifier/v1/admin_pb'

const tokens = ref<Token[]>([])
const channels = ref<Channel[]>([])
const minted = ref<CreateTokenResponse | null>(null)
const newName = ref('')
const newKind = ref('any')
const newChannel = ref('')
const newPrefix = ref('')
// Days until expiry; 0 mints a token that never expires.
const newExpiryDays = ref(0)

async function refresh() {
  try {
    const [t, c] = await Promise.all([api.listTokens({}), api.listChannels({})])
    tokens.value = t.tokens
    channels.value = c.channels
    if (!newChannel.value && channels.value.length) newChannel.value = channels.value[0]!.name
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function create() {
  minted.value = null
  try {
    const resp = await api.createToken({
      name: newName.value.trim(),
      kind: newKind.value,
      channel: newChannel.value,
      prefix: newPrefix.value.trim(),
      ...(newExpiryDays.value > 0 && {
        expiresAt: timestampFromDate(new Date(Date.now() + newExpiryDays.value * 86_400_000)),
      }),
    })
    minted.value = resp
    newName.value = ''
    newPrefix.value = ''
    notifySuccess(`Token "${resp.token?.name}" created`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

// Modal state for the two in-row edits; a non-null target opens the dialog.
const prefixTarget = ref<Token | null>(null)
const prefixValue = ref('')

function editPrefix(tok: Token) {
  prefixValue.value = tok.prefix || ''
  prefixTarget.value = tok
}

async function savePrefix() {
  const tok = prefixTarget.value
  if (!tok) return
  try {
    // Send the current channel so editing the prefix never reroutes the token.
    await api.updateToken({ name: tok.name, prefix: prefixValue.value.trim(), channel: tok.channel })
    notifySuccess(`Prefix updated for "${tok.name}"`)
    prefixTarget.value = null
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function changeChannel(tok: Token, event: Event) {
  const channel = (event.target as HTMLSelectElement).value
  try {
    await api.updateToken({ name: tok.name, prefix: tok.prefix || '', channel })
    notifySuccess(`Token "${tok.name}" now routes to "${channel}"`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
    await refresh()
  }
}

const expiryTarget = ref<Token | null>(null)
const expiryValue = ref('')

function editExpiry(tok: Token) {
  expiryValue.value = ''
  expiryTarget.value = tok
}

async function saveExpiry() {
  const tok = expiryTarget.value
  if (!tok) return
  const days = Number(expiryValue.value.trim())
  if (!Number.isFinite(days) || days < 0) {
    notifyError('Enter a number of days (0 = never expires)')
    return
  }
  try {
    // Prefix and channel ride along unchanged, same as the other edits.
    await api.updateToken({
      name: tok.name,
      prefix: tok.prefix || '',
      channel: tok.channel,
      ...(days === 0
        ? { clearExpiry: true }
        : { expiresAt: timestampFromDate(new Date(Date.now() + days * 86_400_000)) }),
    })
    notifySuccess(days === 0 ? `"${tok.name}" never expires` : `"${tok.name}" now expires in ${days} day(s)`)
    expiryTarget.value = null
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function remove(name: string) {
  if (!confirm(`Delete token "${name}"? Producers using it will get 401s.`)) return
  try {
    await api.deleteToken({ name })
    notifySuccess(`Token "${name}" deleted`)
    await refresh()
  } catch (e) {
    notifyError(errMsg(e))
  }
}

async function test(tok: Token) {
  try {
    await api.testToken({ name: tok.name })
    notifySuccess(`Test notification sent via "${tok.name}" to ${tok.channel}`)
  } catch (e) {
    notifyError(errMsg(e))
  }
}

function copyMinted() {
  if (minted.value) navigator.clipboard?.writeText(minted.value.plaintext)
}

function fmtDate(ts?: Timestamp): string {
  return ts ? timestampDate(ts).toLocaleString() : '—'
}

function isExpired(ts?: Timestamp): boolean {
  return !!ts && timestampDate(ts).getTime() < Date.now()
}

onMounted(refresh)
</script>

<template>
  <Alert v-if="minted" variant="warning" class="mb-4">
    <div class="mb-1 flex items-center gap-1.5 font-semibold">
      <TriangleAlert class="size-4" />Copy this token now — it is shown exactly once:
    </div>
    <div class="flex items-center gap-2">
      <code class="select-all">{{ minted.plaintext }}</code>
      <Button variant="outline" size="icon-sm" @click="copyMinted"><Copy /></Button>
    </div>
  </Alert>

  <Card class="mb-4">
    <template #header><Plus />New token</template>
    <form class="grid grid-cols-1 gap-2 md:grid-cols-6" @submit.prevent="create">
      <Input v-model="newName" placeholder="name (e.g. sonarr)" required />
      <select v-model="newKind" class="select">
        <option value="any">any endpoint</option>
        <option value="gotify">gotify only</option>
        <option value="alertmanager">alertmanager only</option>
        <option value="gitea">gitea/forgejo only</option>
        <option value="slack">slack only</option>
        <option value="grafana">grafana only</option>
      </select>
      <select v-model="newChannel" class="select" required>
        <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
      </select>
      <Input v-model="newPrefix" placeholder="prefix (optional)" title="Prepended to notification titles" />
      <select
        v-model.number="newExpiryDays"
        class="select"
        title="The token stops authenticating past this — rotation means minting a new one"
      >
        <option :value="0">never expires</option>
        <option :value="7">expires in 7 days</option>
        <option :value="30">expires in 30 days</option>
        <option :value="90">expires in 90 days</option>
        <option :value="365">expires in 1 year</option>
      </select>
      <Button type="submit" :disabled="!channels.length">Create</Button>
    </form>
    <p v-if="!channels.length" class="mt-2 text-xs text-amber-400">Create a channel first.</p>
  </Card>

  <Card flush>
    <template #header><KeyRound />Tokens</template>
    <table class="data-table hoverable">
      <thead>
        <tr>
          <th>Name</th><th>Kind</th><th>Channel</th><th>Prefix</th><th>Created</th><th>Last used</th><th>Expires</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="tok in tokens" :key="tok.name">
          <td>{{ tok.name }}</td>
          <td><Badge variant="secondary">{{ tok.kind }}</Badge></td>
          <td>
            <select
              class="select select-sm"
              :value="tok.channel"
              title="Route this token to another room"
              @change="changeChannel(tok, $event)"
            >
              <option v-for="ch in channels" :key="ch.name" :value="ch.name">{{ ch.name }}</option>
            </select>
          </td>
          <td>
            <Button variant="outline" size="sm" title="Edit prefix" @click="editPrefix(tok)">
              {{ tok.prefix || '—' }} <Pencil />
            </Button>
          </td>
          <td class="text-muted-foreground">{{ fmtDate(tok.createdAt) }}</td>
          <td class="text-muted-foreground">{{ fmtDate(tok.lastUsedAt) }}</td>
          <td>
            <Button
              variant="outline"
              size="sm"
              :class="isExpired(tok.expiresAt) ? 'text-red-400' : ''"
              title="Change expiry"
              @click="editExpiry(tok)"
            >
              <span v-if="isExpired(tok.expiresAt)">expired {{ fmtDate(tok.expiresAt) }}</span>
              <template v-else>{{ fmtDate(tok.expiresAt) }}</template>
              <Pencil />
            </Button>
          </td>
          <td class="text-right">
            <div class="inline-flex gap-2">
              <Button
                variant="outline"
                size="icon-sm"
                title="Send a test notification through this token"
                @click="test(tok)"
              >
                <Send />
              </Button>
              <Button variant="outline" size="icon-sm" class="text-red-400" title="Delete" @click="remove(tok.name)">
                <Trash2 />
              </Button>
            </div>
          </td>
        </tr>
        <tr v-if="!tokens.length">
          <td colspan="8" class="py-4 text-center text-muted-foreground">No tokens yet</td>
        </tr>
      </tbody>
    </table>
  </Card>

  <Dialog :open="!!prefixTarget" @close="prefixTarget = null">
    <template #title>Notification prefix — {{ prefixTarget?.name }}</template>
    <form class="space-y-3" @submit.prevent="savePrefix">
      <Input v-model="prefixValue" placeholder="emoji or short text, empty to clear" autofocus />
      <p class="text-xs text-muted-foreground">Prepended to every notification title sent through this token.</p>
      <div class="flex justify-end gap-2">
        <Button variant="outline" @click="prefixTarget = null">Cancel</Button>
        <Button type="submit">Save</Button>
      </div>
    </form>
  </Dialog>

  <Dialog :open="!!expiryTarget" @close="expiryTarget = null">
    <template #title>Token expiry — {{ expiryTarget?.name }}</template>
    <form class="space-y-3" @submit.prevent="saveExpiry">
      <Input v-model="expiryValue" type="number" min="0" placeholder="days from now (0 = never expires)" autofocus required />
      <p class="text-xs text-muted-foreground">
        Currently
        {{ expiryTarget?.expiresAt ? `expires ${fmtDate(expiryTarget?.expiresAt)}` : 'never expires' }}.
        Setting a value counts from now; 0 removes the expiry (and revives an expired token).
      </p>
      <div class="flex justify-end gap-2">
        <Button variant="outline" @click="expiryTarget = null">Cancel</Button>
        <Button type="submit">Save</Button>
      </div>
    </form>
  </Dialog>
</template>
