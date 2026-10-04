import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
const SRC = join(import.meta.dirname, '../../src')
const LAYER_RULES: Record<string, RegExp[]> = {
  lib: [
    /^@\/stores\//,
    /^@\/realtime\//,
    /^@\/components\//,
    /^@\/api\//,
    /^@\/services\//,
    /^@\/hooks\//,
    /^@\/routes\//,
  ],
  api: [
    /^@\/stores\//,
    /^@\/realtime\//,
    /^@\/components\//,
    /^@\/services\//,
    /^@\/hooks\//,
    /^@\/routes\//,
  ],
  realtime: [/^@\/components\//, /^@\/api\//, /^@\/services\//, /^@\/hooks\//, /^@\/routes\//],
  hooks: [/^@\/stores\//, /^@\/components\//, /^@\/api\//, /^@\/services\//, /^@\/routes\//],
  stores: [/^@\/(lib|realtime|components|api|services|hooks|routes)\//],
}
function tsFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...tsFiles(full))
    else if (/\.(ts|tsx)$/.test(entry.name)) out.push(full)
  }
  return out
}
describe('frontend layering rules', () => {
  for (const [layer, forbidden] of Object.entries(LAYER_RULES)) {
    it(`${layer}/ no cross-layer deps (forbidden: ${forbidden.map((r) => r.source.replace('^@\\/', '@/')).join(', ')}})`, () => {
      const violations: string[] = []
      for (const file of tsFiles(join(SRC, layer))) {
        const content = readFileSync(file, 'utf8')
        for (const line of content.split('\n')) {
          const m = line.match(/from '(@\/[^']+)'/)
          if (m && forbidden.some((r) => r.test(m[1]!))) {
            violations.push(`${file.replace(SRC, 'src')}: ${line.trim()}`)
          }
        }
      }
      expect(violations, violations.join('\n')).toEqual([])
    })
  }
})
