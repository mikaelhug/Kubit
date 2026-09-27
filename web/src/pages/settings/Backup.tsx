import { authedUrl } from '../../api'
import { Section } from '../../components/ui'

export function Backup() {
  return (
    <Section title="Kubit backup" help="Sealed archive of Kubit's own state: database, kubeconfigs, talosconfigs, OpenTofu state.">
      <a class="btn btn-primary self-start" href={authedUrl('/backup')} download>Download backup</a>
    </Section>
  )
}
