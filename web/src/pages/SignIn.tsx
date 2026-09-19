import { useState } from 'preact/hooks'
import { api } from '../api'
import { authState, loadMe } from '../store'
import { reconnectLive } from '../live'
import { ErrorBox, Field } from '../components/ui'

/** Full-screen sign-in; on a fresh install it creates the first administrator instead. */
export function SignIn() {
  const setup = authState.value.setup
  const sso = authState.value.sso
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(() => typeof location !== 'undefined' ? new URLSearchParams(location.search).get('sso') : null)
  const [busy, setBusy] = useState(false)
  const submit = (e: Event) => {
    e.preventDefault()
    setBusy(true)
    ;(setup ? api.setup(name, password) : api.login(name, password))
      .then(() => loadMe())
      .then(() => reconnectLive())
      .catch((err) => setError(err.message))
      .finally(() => setBusy(false))
  }
  return (
    <div class="min-h-screen flex items-center justify-center bg-bg p-6">
      <form class="panel p-6 w-full max-w-sm flex flex-col gap-4" onSubmit={submit}>
        <div class="flex items-center gap-2">
          <span class="inline-block h-2.5 w-2.5 rounded-sm bg-accent" />
          <span class="font-semibold tracking-tight">Kubit</span>
        </div>
        <div>
          <h1 class="font-semibold text-lg">{setup ? 'Create the first administrator' : 'Sign in'}</h1>
          {setup && <p class="text-[12.5px] text-muted">No accounts exist yet. This one owns the installation.</p>}
        </div>
        <ErrorBox error={error} />
        <Field label="User"><input class="input mono" autocomplete="username" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} /></Field>
        <Field label="Password" hint={setup ? 'At least 8 characters.' : undefined}><input class="input mono" type="password" autocomplete={setup ? 'new-password' : 'current-password'} value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} /></Field>
        <button class="btn btn-primary" type="submit" disabled={busy || !name || !password}>{setup ? 'Create and sign in' : 'Sign in'}</button>
        {sso && !setup && <a class="btn text-center" href="/api/v1/auth/oidc/start">Sign in with {sso}</a>}
      </form>
    </div>
  )
}
