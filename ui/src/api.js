// Thin client for the Connect JSON protocol: every RPC is a POST of a JSON
// body to /notifier.v1.AdminService/<Method> with the admin bearer token.

const TOKEN_KEY = 'matrix-notifier-admin-token'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token) {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

export class ApiError extends Error {
  constructor(message, code, status) {
    super(message)
    this.code = code
    this.status = status
  }
}

async function call(method, body = {}, fetchFn = fetch) {
  const res = await fetchFn(`/notifier.v1.AdminService/${method}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${getToken()}`,
    },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    let message = `HTTP ${res.status}`
    let code = ''
    try {
      const err = await res.json()
      message = err.message || message
      code = err.code || ''
    } catch {
      // non-JSON error body; keep the HTTP status message
    }
    throw new ApiError(message, code, res.status)
  }
  return res.json()
}

export const api = {
  getStatus: () => call('GetStatus'),
  listChannels: () => call('ListChannels'),
  listRooms: () => call('ListRooms'),
  leaveRoom: (roomId) => call('LeaveRoom', { roomId }),
  createChannel: (name, roomId, chart) => call('CreateChannel', { name, roomId, chart }),
  updateChannel: (name, chart) => call('UpdateChannel', { name, chart }),
  deleteChannel: (name) => call('DeleteChannel', { name }),
  listTokens: () => call('ListTokens'),
  createToken: (name, kind, channel, prefix) => call('CreateToken', { name, kind, channel, prefix }),
  updateToken: (name, prefix) => call('UpdateToken', { name, prefix }),
  deleteToken: (name) => call('DeleteToken', { name }),
  sendTest: (channel) => call('SendTestNotification', { channel }),
}

export { call }
