import js from '@eslint/js'
import prettier from 'eslint-config-prettier'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import globals from 'globals'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  // generated files and deps excluded from lint
  { ignores: ['dist', 'coverage', 'node_modules', 'src/routeTree.gen.ts'] },

  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      // WS frame dispatch is runtime-typed; a few controlled assertions are unavoidable
      '@typescript-eslint/no-explicit-any': 'warn',
    },
  },

  // File-based routing: Route exports + local components are the TanStack Router
  // standard shape; its Vite plugin manages route-file HMR boundaries, so the
  // react-refresh rule does not apply
  {
    files: ['src/routes/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },

  // Node environment (vite.config.ts)
  {
    files: ['vite.config.ts'],
    languageOptions: {
      globals: globals.node,
    },
  },

  // test environment
  {
    files: ['test/**/*.ts', 'test/**/*.tsx'],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
  },

  prettier,
)
