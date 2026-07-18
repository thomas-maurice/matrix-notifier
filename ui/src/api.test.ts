import { describe, expect, it } from 'vitest'
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import { AdminService } from './gen/notifier/v1/admin_pb'
import { errMsg, isUnauthenticated, makeApi } from './api'

describe('api', () => {
  // The generated client must round-trip typed messages against the real
  // service schema — a drifted proto would fail here at compile time.
  it('round-trips typed calls through the service descriptor', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(AdminService, {
        listChannels: () => ({ channels: [{ name: 'infra', roomId: '!r:x' }] }),
      })
    })
    const api = makeApi(transport)
    const resp = await api.listChannels({})
    expect(resp.channels).toHaveLength(1)
    expect(resp.channels[0]!.name).toBe('infra')
    expect(resp.channels[0]!.roomId).toBe('!r:x')
  })

  // Delivery ids are proto int64 → bigint on the wire; the History panel's
  // retry button must round-trip them without precision loss.
  it('round-trips delivery history with bigint ids', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(AdminService, {
        listDeliveries: () => ({
          deliveries: [{ id: 42n, channel: 'infra', kind: 'gotify', status: 'failed', lastError: 'boom' }],
        }),
        retryDelivery: (req) => {
          expect(req.id).toBe(42n)
          return {}
        },
      })
    })
    const api = makeApi(transport)
    const resp = await api.listDeliveries({ channel: 'infra' })
    expect(resp.deliveries[0]!.status).toBe('failed')
    await api.retryDelivery({ id: resp.deliveries[0]!.id })
  })

  // Toasts must show real causes ("invalid password"), not Connect's
  // "[unauthenticated] invalid password" wire format — and the login form
  // needs to distinguish 401 from the server being unreachable.
  it('surfaces bare error messages and detects unauthenticated', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(AdminService, {
        login: () => {
          throw new ConnectError('invalid password', Code.Unauthenticated)
        },
      })
    })
    const api = makeApi(transport)
    let caught: unknown
    try {
      await api.login({ password: 'nope' })
    } catch (e) {
      caught = e
    }
    expect(caught).toBeInstanceOf(ConnectError)
    expect(errMsg(caught)).toBe('invalid password')
    expect(isUnauthenticated(caught)).toBe(true)
    expect(isUnauthenticated(new Error('network down'))).toBe(false)
  })
})
