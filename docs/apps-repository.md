# Apps repository

Flux in each cluster syncs your workloads from an apps repository you own. Kubit writes the
layout Flux's own guide recommends
([repository structure](https://fluxcd.io/flux/guides/repository-structure/)): one repository
for every cluster, one folder per cluster that tells Flux what to apply.

```
apps/
├── .sops.yaml                  who can decrypt which secrets
├── infrastructure/
│   ├── base/<component>/       cluster-wide config you write once
│   ├── staging/                the components the cluster named staging runs
│   └── production/
├── apps/
│   ├── base/<app>/             each app: Deployment or HelmRelease, Service, route
│   ├── staging/                lists ../base/<app> per app it runs, plus what differs:
│   │                           image tag, replicas, hostname, secrets
│   └── production/
└── clusters/
    ├── staging/                platform.flux.repository.path of that cluster
    │   ├── kustomization.yaml
    │   ├── infrastructure.yaml Flux Kustomization → ./infrastructure/staging
    │   └── apps.yaml           Flux Kustomization → ./apps/staging, after infrastructure
    └── production/
```

Each environment is its own cluster, on its own machines: a bad deploy or an upgrade tried on
staging never touches production. Name clusters after their environment (`staging`,
`production`); the folders take the cluster's name.

Kubit writes `clusters/<cluster>/`, an empty `apps/<cluster>/` and `infrastructure/<cluster>/`,
and `.sops.yaml`. You write each app once under `apps/base/<app>/` and turn it on per cluster
by listing `../base/<app>` in that cluster's folder, so an app reaches staging first and
production when you list it there (a pull request, if you work that way). Software Kubit installs as
an add-on (cert-manager, Traefik, MetalLB, Longhorn) stays in `cluster.yaml`, never in
`infrastructure/`. There is no `clusters/<cluster>/flux-system` folder: Kubit installs Flux
itself.

`.sops.yaml` has a rule per cluster, `^(apps|infrastructure)/<cluster>/.*\.sops\.ya?ml$`,
encrypting to your key plus that cluster's Flux key. Staging cannot decrypt production's
secrets. Secrets elsewhere are encrypted to your key only.

## Connecting a cluster

You clone the repository yourself (`git clone`, any remote form, ssh aliases included). Then
either:

- **New cluster** (Discovery › Add): fill *Apps repository* with the checkout.
- **Existing cluster** (Settings › Apps › *Connect* or *Change*), the same field.
- **CLI**: `kubit init production --nodes … --apps ~/git/apps` or
  `kubit connect production ~/git/apps`.

The review lists every file Kubit writes in the checkout. Writing sets
`platform.flux.repository` (URL from the checkout's `origin`, ssh aliases resolved through
`ssh -G`; a private https remote becomes its ssh form), adds the cluster's Flux key to its
rule, creates a deploy key for ssh URLs (see *Private apps repo* in
[Cluster repo](cluster-yaml.md)), and serves the checkout. A checkout whose layout is not
Kubit's only gets a Flux path; nothing is written into it.

Until you push, the plan lists *Apps repository: commit and push clusters/<cluster>, … to
origin/main*; with an ssh URL it also lists the deploy key until the host accepts it. Both link
to Settings › Apps, which shows the repository, Flux path, the checkout's git state, the
deploy key with *Add to GitHub*, and the SOPS recipient. A push re-plans by itself.

`platform.flux.repository.checkout` records where the checkout sits, relative to the cluster
repo (`../../akrell-apps`), so `kubit ~/akrell/akrell-cluster` serves every cluster in its
subfolders and their checkout. The first run needs the checkout once:
`kubit ~/akrell/akrell-cluster ~/akrell/akrell-apps`; *Add* then creates `staging` and
`production` as `~/akrell/akrell-cluster/staging` and `…/production`. Flux, plan and apply ignore it;
a checkout that is not there shows *not found* in Settings › Apps.

## Image automation

With `platform.flux.imageAutomation: true` (Settings › Apps › *Image automation*), Flux in the
cluster watches each app's image in its registry and commits the newest tag its policy allows
into the apps repository; the normal sync then rolls it out. Nothing is needed in the app's own
repository, so third-party images work the same way. Turning it on installs Flux's image
controllers and writes `clusters/<cluster>/image-automation.yaml` (an `ImageUpdateAutomation`
for `./apps/<cluster>`, committing as `flux-<cluster>`); turning it off removes it. The deploy key
needs write access; the plan reports a key that cannot push.

Per app, `apps/base/<app>/` holds an `ImageRepository`, and each cluster's folder an
`ImagePolicy` and the tag to update:

```yaml
images:
  - name: ghcr.io/you/app
    newTag: 0.4.0-rc.12 # {"$imagepolicy": "flux-system:app:tag"}
```

A semver range per cluster gives the usual flow: staging `>=0.0.0-0` takes the `-rc` builds from
`main`, production `>=0.0.0` only releases.

## Secrets

The Secrets tab follows the cluster's Flux Kustomizations, so a secret under `apps/<cluster>`
counts as applied when every `kustomization.yaml` on its way lists it. *New Secret* defaults to
`apps/<cluster>/<namespace or name>/<name>.sops.yaml` (namespace `default` when left empty, as
Flux needs one), creates the folder's `kustomization.yaml` and lists the folder in its parent;
*Delete* undoes both. *Let Flux decrypt* adds a cluster's key only to its own rule.
