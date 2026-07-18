// Typed Connect client for the admin API, built on the buf-generated
// service descriptor (src/gen). Auth lives in an httpOnly session cookie:
// same-origin requests carry it automatically and no token ever touches JS.
import { Code, ConnectError, createClient, type Transport } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { AdminService } from './gen/notifier/v1/admin_pb'

// makeApi exists so tests can inject a router transport; the app uses the
// default export below. The window guard keeps the module importable under
// Node (vitest), where the default transport is never actually used.
export function makeApi(transport?: Transport) {
  const t =
    transport ??
    createConnectTransport({ baseUrl: typeof window === 'undefined' ? 'http://unused.invalid' : window.location.origin })
  return createClient(AdminService, t)
}

export const api = makeApi()

// errMsg unwraps a ConnectError to its bare message ("channel exists", not
// "[already_exists] channel exists") so toasts show real causes.
export function errMsg(e: unknown): string {
  if (e instanceof ConnectError) return e.rawMessage
  return e instanceof Error ? e.message : String(e)
}

export function isUnauthenticated(e: unknown): boolean {
  return e instanceof ConnectError && e.code === Code.Unauthenticated
}
