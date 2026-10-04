import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
export const server = setupServer(
  http.get('/config', () => HttpResponse.json({ version: 'test', csrf_token: 'csrf-from-config' })),
)
