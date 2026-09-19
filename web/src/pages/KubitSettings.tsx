import { useLocation } from 'preact-iso'
import { useEffect } from 'preact/hooks'
import { can } from '../store'
import { settingsPages, type SettingsPage } from '../app'
import { UsersSection } from '../components/Users'
import { SettingsLayout } from './settings/Layout'
import { General } from './settings/General'
import { Discovery } from './settings/Discovery'
import { Alerts } from './settings/Alerts'
import { Offsite } from './settings/Offsite'
import { Sso } from './settings/Sso'
import { Backup } from './settings/Backup'

/** /settings/:page — one page of this installation's settings. */
export function KubitSettings({ page }: { page?: string }) {
  const { route } = useLocation()
  const known = settingsPages.some(([id]) => id === page)
  const allowed = known && ((page !== 'accounts' && page !== 'sso') || can('admin'))
  useEffect(() => { if (!allowed) route('/settings/general', true) }, [allowed, route])
  if (!allowed) return null
  const p = page as SettingsPage
  return (
    <SettingsLayout page={p}>
      {p === 'general' && <General />}
      {p === 'discovery' && <Discovery />}
      {p === 'alerts' && <Alerts />}
      {p === 'offsite' && <Offsite />}
      {p === 'accounts' && <UsersSection />}
      {p === 'sso' && <Sso />}
      {p === 'backup' && <Backup />}
    </SettingsLayout>
  )
}
