# Kubit

Stack, layout, commands and design: [README.md](README.md), the source of truth; keep it
current. Open work goes to [NOTES/backlog.md](NOTES/backlog.md).

The rules below override your defaults. Follow them unless I say otherwise in the session.

## 1. No comments

Write none, in Go, TypeScript, Terraform or YAML templates. The only exception is a fact this
repository cannot tell the reader:

- behaviour of a system we do not own (Talos, etcd, Flux, SOPS, OpenTofu, a library bug)
- a magic value copied from outside, with its source

One line. Never write what the next line does, a note to the reviewer ("now", "replaces",
"fixed"), a defence of a design, a banner, or a doc comment that restates a name or
signature. Doc comments are comments.

## 2. No explanatory text in the console

Every word on screen is read. A screen carries data, labels and actions, nothing that explains
them:

- no section help lines, field hints or tooltips that restate the label or the obvious
- no "Apply will…" or "this writes…" lines in dialogs; the action label says it
  (*Write cluster.yaml*)
- a notice states a state or a problem in one short sentence ("Flux is off.")
- keep a hint only when the field is unusable without it ("Optional", a validation error)
- keep a warning only for data loss ("Apply erases each install disk.")
- no ellipses in action labels

Background, reasons and examples belong in README.md.

## 3. No tests without a motivation

Do not add a test because you changed code. A new test needs one of these motivations, stated
in its name or first line:

- it reproduces a bug that actually happened and would come back unseen
- it pins behaviour of a system we do not own (Talos machinery, the etcd gateway, the sops
  CLI, Flux kustomize rules, Kubernetes validation)
- it guards a safety property: secrets never written in clear, a stale edit refused, a plan
  hash mismatch refused, a destructive step gated

Even then, propose it in one line and wait for a yes. Prefer a test through the API or the CLI
(a repo in, a plan or a file out) over a unit test of a helper.

Never write tests of constants, getters or formatting, a return value restated, mocks
asserting they were called, one test per branch, or edge-case sweeps.

When your change breaks an existing test: fix it if it guards one of the motivations above;
delete it if it only restated the old code, and list every deletion in your reply.

## 4. Before you finish

Re-read your diff. Delete every comment that fails §1, every string that fails §2 and every
test you added that fails §3. Expect that to be most of them.

## 5. Scope and consent

- Do what I asked, then stop. Name adjacent problems in one line at the end of your reply;
  don't fix them.
- No unrequested docs, renames or formatting passes. No CI or packaging work unless asked.
- Reuse before you add: search for an existing component, helper or package first. Duplicate
  logic is a bug; factor it out the second time, inside your scope. No speculative
  optimisation.
- Ask first, in two or three sentences with the trade-off, before you:
  - touch more than a handful of files
  - move, rename or split a package
  - change the cluster.yaml schema or the meaning of a field
  - add a dependency (Go module or npm package)
  - act on a real cluster or repo: Apply, a write to `~/lab`, a node move, a reset
- I commit. Never run git commit, push or stash.

## 6. Architecture

- The cluster repo is the source of truth: `cluster.yaml`, `secrets.sops.yaml` and the files
  beside them. Every change goes through plan and apply. No database; the daemon keeps a cache
  in memory only.
- One code path, the production one. No environment sniffing: configuration supplies values,
  never behaviour. Kubit runs on a laptop that sleeps; nothing assumes an always-on host.
- Event-driven: the daemon pushes typed messages on the single `/api/v1/ws` stream and views
  refetch only on a `refresh` for their scope. No timers that poll the API. Anything
  time-based in the console ticks from `web/src/clock.ts`.
- Every view is live: daemon state appears without a page reload.
- Edits to repo files go through the YAML tree (comments and order survive), are validated
  like `kubit plan` validates, are refused when the file changed since it was read, and are
  written atomically.
