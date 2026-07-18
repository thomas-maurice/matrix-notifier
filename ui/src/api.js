// Thin client for the Connect JSON protocol: every RPC is a POST of a JSON
// body to /notifier.v1.AdminService/<Method>. Authentication rides in an
// httpOnly session cookie set by Login — the token itself is never visible
// to (or stored by) this code.

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
    headers: { 'Content-Type': 'application/json' },
    // The session cookie must ride along on every RPC.
    credentials: 'same-origin',
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
  login: (password) => call('Login', { password }),
  logout: () => call('Logout'),
  changePassword: (currentPassword, newPassword) =>
    call('ChangeAdminPassword', { currentPassword, newPassword }),
  getStatus: () => call('GetStatus'),
  listChannels: () => call('ListChannels'),
  listRooms: () => call('ListRooms'),
  leaveRoom: (roomId) => call('LeaveRoom', { roomId }),
  createChannel: (name, roomId, chart) => call('CreateChannel', { name, roomId, chart }),
  updateChannel: (name, chart) => call('UpdateChannel', { name, chart }),
  deleteChannel: (name) => call('DeleteChannel', { name }),
  listTokens: () => call('ListTokens'),
  createToken: (name, kind, channel, prefix) => call('CreateToken', { name, kind, channel, prefix }),
  updateToken: (name, prefix, channel) => call('UpdateToken', { name, prefix, channel }),
  deleteToken: (name) => call('DeleteToken', { name }),
  testToken: (name) => call('TestToken', { name }),
  sendTest: (channel) => call('SendTestNotification', { channel }),
  getProfile: () => call('GetProfile'),
  // avatar is base64-encoded bytes (Connect JSON encoding for proto bytes).
  setProfile: (displayName, avatar) => call('SetProfile', { displayName, avatar }),
}

export { call }
