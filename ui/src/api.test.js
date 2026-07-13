import { describe, it, expect, beforeEach, vi } from 'vitest'
import { call, setToken, ApiError } from './api.js'

// localStorage shim for the node test environment.
const storage = new Map()
globalThis.localStorage = {
  getItem: (k) => storage.get(k) ?? null,
  setItem: (k, v) => storage.set(k, v),
  removeItem: (k) => storage.delete(k),
}

beforeEach(() => storage.clear())

describe('call', () => {
  it('POSTs JSON to the Connect endpoint with the bearer token', async () => {
    setToken('sekrit')
    const fetchFn = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: 1 }) })
    const resp = await call('GetStatus', { a: 1 }, fetchFn)
    expect(resp).toEqual({ ok: 1 })
    const [url, opts] = fetchFn.mock.calls[0]
    expect(url).toBe('/notifier.v1.AdminService/GetStatus')
    expect(opts.method).toBe('POST')
    // The admin token must ride along or every RPC 401s.
    expect(opts.headers.Authorization).toBe('Bearer sekrit')
    expect(JSON.parse(opts.body)).toEqual({ a: 1 })
  })

  it('surfaces Connect error messages so the UI can show real causes', async () => {
    const fetchFn = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({ code: 'already_exists', message: 'channel "x" exists' }),
    })
    await expect(call('CreateChannel', {}, fetchFn)).rejects.toThrowError(ApiError)
    await expect(call('CreateChannel', {}, fetchFn)).rejects.toThrow('channel "x" exists')
  })
})
