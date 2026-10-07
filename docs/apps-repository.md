# Apps repository

Flux in each cluster syncs your workloads from an apps repository you own. Kubit writes the
layout Flux's own guide recommends
([repository structure](https://fluxcd.io/flux/guides/repository-structure/)): one repository
for every cluster, environments as folders, one folder per cluster that tells Flux what to
apply.

```
apps/
├── .sops.yaml                  who can decrypt which secrets
├── infrastructure/
│   ├── base/                   cluster-wide config shared by all environments
│   ├── staging/                what differs in staging (lists ../base)
│   └── production/
├── apps/
│   ├── base/                   your apps: Deployment or HelmRelease, Service, route
│   ├── staging/                what differs in staging: image tag, replicas, hostname
│   └── production/
└── clusters/
    ├── akrell-staging/         platform.flux.repository.path of that cluster
    │   ├── kustomization.yaml
    │   ├── infrastructure.yaml Flux Kustomization → ./infrastructure/staging
    │   └── apps.yaml           Flux Kustomization → ./apps/staging, after infrastructure
    └── akrell-production/
```

Kubit writes the folders, the `kustomization.yaml` files, `clusters/<cluster>/` and
`.sops.yaml`; you write the apps and the differences between environments, and release to
production by changing `apps/production` (a pull request, if you work that way). Software
Kubit installs as an add-on (cert-manager, Traefik, MetalLB, Longhorn) stays in
`cluster.yaml`, never in `infrastructure/`. There is no `clusters/<cluster>/flux-system`
folder: Kubit installs Flux itself.

## Environments

An environment is a folder pair, `apps/<env>` and `infrastructure/<env>`. A cluster serves one
environment; several clusters may serve the same one. One environment is the simple case;
`test`, `staging` and `production` are offered, any lowercase name works. The environment is
not a `cluster.yaml` field: the cluster's Flux path points at `clusters/<cluster>`, whose two
Flux Kustomizations name the environment.

`.sops.yaml` has a rule per environment, `^(apps|infrastructure)/<env>/.*\.sops\.ya?ml$`,
encrypting to your key plus the Flux keys of the clusters serving that environment. Staging's
cluster cannot decrypt production's secrets. Secrets outside the environment folders are
encrypted to your key only.

## Connecting a cluster

You clone the repository yourself (`git clone`, any remote form, ssh aliases included). Then
either:

- **New cluster** (Discovery › Add): fill *Apps repository* with the checkout and pick the
  environments (an empty checkout) or the environment (an existing layout).
- **Existing cluster** (Settings › Apps › *Connect* or *Change*), the same fields.
- **CLI**: `kubit init lab --nodes … --apps ~/git/apps --env production [--envs staging,production]`
  or `kubit connect lab ~/git/apps --env production`.

The review lists every file Kubit writes in the checkout. Writing sets
`platform.flux.repository` (URL from the checkout's `origin`, ssh aliases resolved through
`ssh -G`; a private https remote becomes its ssh form), adds the cluster's Flux key to its
environment's rule, creates a deploy key for ssh URLs (see
*Private apps repo* in [Cluster repo](cluster-yaml.md)), and serves the checkout. A
checkout whose layout is not Kubit's only gets a Flux path; nothing is written into it.

Until you push, the plan lists *Apps repository: commit and push clusters/<cluster>, … to
origin/main*; with an ssh URL it also lists the deploy key until the host accepts it. Both link
to Settings › Apps, which shows the repository, Flux path, environment, the checkout's git
state, the deploy key with *Add to GitHub*, and the SOPS recipient. A push re-plans by itself.

Kubit serves the checkout for the session; after a restart pass it along:
`kubit ~/git/akrell-cluster/production ~/git/apps`.

## Secrets

The Secrets tab follows the cluster's Flux Kustomizations, so a secret under `apps/<env>` counts
as applied when every `kustomization.yaml` on its way lists it. *New Secret* defaults to
`apps/<env>/<namespace or name>/<name>.sops.yaml` (namespace `default` when left empty, as Flux
needs one), creates the folder's `kustomization.yaml` and lists the folder in its parent;
*Delete* undoes both. *Let Flux decrypt* adds a cluster's key only to the rule of its own
environment.
