import { describe, it, expect, vi } from 'vitest'
import { call, ApiError } from './api.js'

describe('call', () => {
  it('POSTs JSON with the session cookie riding along, never a stored token', async () => {
    const fetchFn = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: 1 }) })
    const resp = await call('GetStatus', { a: 1 }, fetchFn)
    expect(resp).toEqual({ ok: 1 })
    const [url, opts] = fetchFn.mock.calls[0]
    expect(url).toBe('/notifier.v1.AdminService/GetStatus')
    expect(opts.method).toBe('POST')
    // Auth lives in an httpOnly cookie: it must be sent with the request,
    // and no Authorization header (from any client-side storage) may exist.
    expect(opts.credentials).toBe('same-origin')
    expect(opts.headers.Authorization).toBeUndefined()
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
