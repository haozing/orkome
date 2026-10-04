import { Languages } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import i18n, { LOCALE_STORAGE_KEY, type AppLocale } from '@/i18n'
export function LanguageToggle() {
  const { t } = useTranslation()
  const next: AppLocale = i18n.language === 'zh' ? 'en' : 'zh'
  const switchLocale = () => {
    void i18n.changeLanguage(next)
    try {
      localStorage.setItem(LOCALE_STORAGE_KEY, next)
    } catch {
      /* localStorage unavailable */
    }
  }
  return (
    <Button variant="ghost" size="sm" aria-label={t('common.language')} onClick={switchLocale}>
      <Languages className="size-4" />
      <span className="text-xs font-medium">{next.toUpperCase()}</span>
    </Button>
  )
}
