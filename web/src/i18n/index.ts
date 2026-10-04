import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from './locales/en'
import zh from './locales/zh'

export type AppLocale = 'en' | 'zh'
export const LOCALE_STORAGE_KEY = 'orkome.locale'
export const locales: AppLocale[] = ['en', 'zh']

function detectLocale(): AppLocale {
  try {
    const stored = localStorage.getItem(LOCALE_STORAGE_KEY)
    if (stored && locales.includes(stored as AppLocale)) return stored as AppLocale
  } catch {
    /* localStorage unavailable */
  }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function applyLocale(locale: AppLocale) {
  document.documentElement.lang = locale
}

void i18n.use(initReactI18next).init({
  resources: {
    en: { translation: en },
    zh: { translation: zh },
  },
  lng: detectLocale(),
  fallbackLng: 'en',
  interpolation: { escapeValue: false },
})

applyLocale(i18n.language as AppLocale)

i18n.on('languageChanged', (lng) => applyLocale(lng as AppLocale))

export default i18n
