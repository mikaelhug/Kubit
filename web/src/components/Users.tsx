import { useEffect, useState } from 'preact/hooks'
import { api, fmt, type ApiToken, type Role, type User } from '../api'
import { authState, loadMe, me, refreshKey, toast } from '../store'
import { DataTable, type Column } from './DataTable'
import { reconnectLive } from '../live'
import { ConfirmDialog, Dialog, ErrorBox, Field, Notice, Pill, Section } from './ui'

const roles: Role[] = ['viewer', 'operator', 'admin']
const roleHelp: Record<Role, string> = { viewer: 'Read everything except credentials.', operator: 'Run operations: create, add, upgrade, power.', admin: 'Everything, including accounts and settings.' }

/** Accounts, roles and API tokens; administrators only. */
export function UsersSection() {
  const [users, setUsers] = useState<User[]>([])
  const [error, setError] = useState<string | null>(null)
  const [add, setAdd] = useState(false)
  const [edit, setEdit] = useState<User | null>(null)
  const [tokensFor, setTokensFor] = useState<User | null>(null)
  const [remove, setRemove] = useState<User | null>(null)
  const load = () => api.users().then((u) => { setUsers(u); setError(null) }).catch((e) => setError(e.message))
  useEffect(() => { load() }, [refreshKey('', 'users')]) // eslint-disable-line
  const columns: Column<User>[] = [
    { id: 'name', header: 'User', sort: (u) => u.name, mono: true, cell: (u) => <span class="flex items-center gap-2">{u.name}{u.name === me.value?.user && <Pill tone="info">you</Pill>}{u.disabled && <Pill tone="muted">disabled</Pill>}</span> },
    { id: 'role', header: 'Role', sort: (u) => u.role, cell: (u) => u.role },
    { id: 'source', header: 'Source', sort: (u) => u.source, cell: (u) => u.source },
    { id: 'login', header: 'Last sign-in', sort: (u) => u.lastLogin ?? '', cell: (u) => <span class="num text-muted">{u.lastLogin ? fmt.when(u.lastLogin) : 'never'}</span> },
    { id: 'actions', header: '', align: 'right', cell: (u) => <span class="flex gap-1 justify-end"><button class="btn !py-0.5 !px-2 text-[12px]" onClick={() => setTokensFor(u)}>Tokens</button><button class="btn !py-0.5 !px-2 text-[12px]" onClick={() => setEdit(u)}>Edit</button><button class="btn !py-0.5 !px-2 text-[12px]" onClick={() => setRemove(u)}>Delete</button></span> },
  ]
  return (
    <Section title="Accounts" help="Who can sign in and what they may do. Every action is recorded with its account in the audit log." actions={<button class="btn btn-primary" onClick={() => setAdd(true)}>+ Add account</button>}>
      <ErrorBox error={error} />
      {authState.value.setup && <Notice tone="warn">No accounts exist: anyone reaching this address is an administrator. Add the first account to require sign-in.</Notice>}
      <DataTable id="users" search={false} columns={columns} rows={users} rowKey={(u) => u.name} defaultSort={{ id: 'name', dir: 'asc' }} empty="No accounts." />
      {add && <UserDialog onClose={() => { setAdd(false); loadMe().then(() => { reconnectLive(); load() }) }} />}
      {edit && <UserDialog user={edit} onClose={() => { setEdit(null); load() }} />}
      {tokensFor && <TokensDialog user={tokensFor} onClose={() => setTokensFor(null)} />}
      {remove && <ConfirmDialog title={`Delete ${remove.name}`} action="Delete account" tone="danger" onClose={() => setRemove(null)} onConfirm={() => api.deleteUser(remove.name).then(() => { setRemove(null); load(); toast('Account deleted', 'good') }).catch((e) => { setRemove(null); toast(e.message, 'error') })} impact={<p>Signs the account out everywhere and revokes its API tokens. Audit entries keep the name.</p>} />}
    </Section>
  )
}

function UserDialog({ user, onClose }: { user?: User; onClose: () => void }) {
  const [name, setName] = useState(user?.name ?? '')
  const [password, setPassword] = useState('')
  const first = !user && authState.value.setup
  const [role, setRole] = useState<Role>(user?.role ?? (first ? 'admin' : 'operator'))
  const [disabled, setDisabled] = useState(user?.disabled ?? false)
  const [error, setError] = useState<string | null>(null)
  const submit = () => {
    const p = user ? api.updateUser(user.name, { role, disabled, password: password || undefined }) : first ? api.setup(name, password) : api.createUser(name, password, role)
    p.then(() => { toast(user ? 'Account updated' : first ? 'Account created; you are signed in as it' : 'Account created', 'good'); onClose() }).catch((e) => setError(e.message))
  }
  return (
    <Dialog title={user ? `Edit ${user.name}` : 'Add account'} onClose={onClose} footer={<><button class="btn" onClick={onClose}>Cancel</button><button class="btn btn-primary" disabled={!name || (!user && password.length < 8)} onClick={submit}>{user ? 'Save' : 'Create'}</button></>}>
      <ErrorBox error={error} />
      <div class="grid grid-cols-2 gap-3">
        <Field label="User"><input class="input mono" value={name} disabled={!!user} onInput={(e) => setName((e.target as HTMLInputElement).value.toLowerCase())} /></Field>
        <Field label={user ? 'New password' : 'Password'} hint={user ? 'Empty keeps the current one.' : 'At least 8 characters.'}><input class="input mono" type="password" value={password} onInput={(e) => setPassword((e.target as HTMLInputElement).value)} /></Field>
        <Field label="Role" hint={first ? 'The first account is the administrator.' : roleHelp[role]}>
          <select class="input" value={role} disabled={first} onChange={(e) => setRole((e.target as HTMLSelectElement).value as Role)}>{roles.map((r) => <option key={r} value={r}>{r}</option>)}</select>
        </Field>
        {user && <label class="flex items-center gap-2 text-[13px] self-end pb-2"><input type="checkbox" checked={disabled} onChange={(e) => setDisabled((e.target as HTMLInputElement).checked)} /> Disabled</label>}
      </div>
    </Dialog>
  )
}

function TokensDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const [tokens, setTokens] = useState<ApiToken[]>([])
  const [name, setName] = useState('')
  const [days, setDays] = useState(90)
  const [issued, setIssued] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = () => api.tokens(user.name).then(setTokens).catch((e) => setError(e.message))
  useEffect(() => { load() }, [user.name]) // eslint-disable-line
  const create = () => api.createToken(user.name, name, days).then((r) => { setIssued(r.token); setName(''); load() }).catch((e) => setError(e.message))
  return (
    <Dialog title={`API tokens for ${user.name}`} onClose={onClose} footer={<button class="btn" onClick={onClose}>Close</button>}>
      <ErrorBox error={error} />
      <p class="text-[13px] text-muted">Bearer tokens with this account's role, for scripts and the PXE service.</p>
      {issued && <Notice tone="good"><span class="flex flex-col gap-1"><span>Copy it now; it is not shown again.</span><code class="mono text-[12px] break-all select-all">{issued}</code></span></Notice>}
      <div class="flex flex-col gap-1 text-[13px]">
        {tokens.length === 0 && <span class="text-muted">No tokens.</span>}
        {tokens.map((t) => <span key={t.name} class="flex items-center gap-2"><span class="mono">{t.name}</span><span class="text-muted">{t.expiresAt ? `expires ${fmt.when(t.expiresAt)}` : 'no expiry'}{t.lastUsed ? ` · used ${fmt.when(t.lastUsed)}` : ''}</span><button class="btn !py-0.5 !px-2 text-[12px] ml-auto" onClick={() => api.deleteToken(user.name, t.name).then(load).catch((e) => setError(e.message))}>Revoke</button></span>)}
      </div>
      <div class="grid grid-cols-[1fr_auto_auto] gap-2 items-end">
        <Field label="Name"><input class="input mono" value={name} onInput={(e) => setName((e.target as HTMLInputElement).value)} /></Field>
        <Field label="Days"><input class="input num w-20" type="number" min={0} value={days} onInput={(e) => setDays(Number((e.target as HTMLInputElement).value))} /></Field>
        <button class="btn btn-primary" disabled={!name} onClick={create}>Issue</button>
      </div>
    </Dialog>
  )
}
