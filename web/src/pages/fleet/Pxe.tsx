import { EmptyState } from '../../components/ui'

export function Pxe() {
  return (
    <div class="p-6">
      <EmptyState title="PXE boot — not built yet (M6)">
        The daemon can already serve iPXE (<span class="mono">kubit pxe</span>): proxyDHCP, TFTP and an HTTP cache of Image Factory assets. This page will show the server state, the boot log per MAC and an adopt flow.
      </EmptyState>
    </div>
  )
}
