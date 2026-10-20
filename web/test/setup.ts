import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import i18n from '@/i18n'
import { server } from './msw'

// jsdom 不实现 matchMedia；kit 的响应式 hooks（use-mobile，见 @ploykit/ui 的
// WorkspaceSwitcher）依赖它。桌面判定默认 false（isMobile=false），与测试断言的
// 桌面形态一致。
if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}

void i18n.changeLanguage('en')
afterEach(() => cleanup())
beforeAll(() => server.listen({ onUnhandledFrame: 'error' }))
afterEach(() => {
  server.resetHandlers()
})
afterAll(() => server.close())
