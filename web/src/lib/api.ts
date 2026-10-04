export interface ApiErrorEnvelope {
  error: {
    code: string
    message: string
    details?: unknown
  }
}
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details?: unknown
  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}
let csrfToken: string | null = null
let csrfBootstrap: Promise<void> | null = null
type WorkspaceIdProvider = () => string | null
let workspaceIdProvider: WorkspaceIdProvider = () => null
export function setWorkspaceIdProvider(provider: WorkspaceIdProvider): void {
  workspaceIdProvider = provider
}
function ensureCsrfToken(): Promise<void> {
  if (csrfToken) return Promise.resolve()
  csrfBootstrap ??= fetch('/config', {
    method: 'GET',
    credentials: 'same-origin',
  })
    .then(async (res) => {
      if (!res.ok) throw new Error(`CSRF bootstrap failed: ${res.status}`)
      const data = (await res.json()) as {
        csrf_token?: string
      }
      if (!data.csrf_token) throw new Error('CSRF bootstrap: response missing csrf_token')
      csrfToken = data.csrf_token
    })
    .catch((err: unknown) => {
      csrfBootstrap = null
      throw err
    })
  return csrfBootstrap
}
function maybeRefreshCsrf(data: unknown): void {
  if (
    typeof data === 'object' &&
    data !== null &&
    'csrf_token' in data &&
    typeof (
      data as {
        csrf_token: unknown
      }
    ).csrf_token === 'string'
  ) {
    csrfToken = (
      data as {
        csrf_token: string
      }
    ).csrf_token
  }
}
export interface ApiFetchOptions extends Omit<RequestInit, 'body'> {
  body?: unknown
}
export async function apiFetch<T>(path: string, options: ApiFetchOptions = {}): Promise<T> {
  const method = options.method?.toUpperCase() ?? 'GET'
  if (method !== 'GET' && method !== 'HEAD') {
    await ensureCsrfToken()
  }
  const headers = new Headers(options.headers)
  const isFormData = typeof FormData !== 'undefined' && options.body instanceof FormData
  if (options.body !== undefined && !isFormData && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (csrfToken && method !== 'GET' && method !== 'HEAD') {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const workspaceId = workspaceIdProvider()
  if (workspaceId && path.startsWith('/api')) {
    headers.set('X-Workspace-Id', workspaceId)
  }
  const res = await fetch(path, {
    ...options,
    method,
    credentials: 'same-origin',
    headers,
    body:
      options.body === undefined
        ? undefined
        : isFormData
          ? (options.body as FormData)
          : JSON.stringify(options.body),
  })
  if (res.status === 204) return undefined as T
  let data: unknown
  try {
    data = await res.json()
  } catch {
    if (res.ok) return undefined as T
    throw new ApiError(res.status, 'E_MALFORMED_RESPONSE', `non-JSON response (${res.status})`)
  }
  if (!res.ok) {
    const envelope = data as ApiErrorEnvelope
    if (envelope?.error?.code) {
      throw new ApiError(
        res.status,
        envelope.error.code,
        envelope.error.message,
        envelope.error.details,
      )
    }
    throw new ApiError(
      res.status,
      'E_MALFORMED_RESPONSE',
      `HTTP ${res.status} without error envelope`,
    )
  }
  maybeRefreshCsrf(data)
  return data as T
}
export const api = {
  get: <T>(path: string) => apiFetch<T>(path),
  post: <T>(path: string, body?: unknown) => apiFetch<T>(path, { method: 'POST', body }),
  put: <T>(path: string, body?: unknown) => apiFetch<T>(path, { method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown) => apiFetch<T>(path, { method: 'PATCH', body }),
  delete: <T>(path: string, body?: unknown) => apiFetch<T>(path, { method: 'DELETE', body }),
}
export function __resetApiForTests(): void {
  csrfToken = null
  csrfBootstrap = null
}
