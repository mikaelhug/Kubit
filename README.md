<div align="center">

<img src="web/public/favicon.svg" width="72" height="72" alt="Kubit logo" />

# Kubit

**Kubernetes on your own machines, kept in git.**

Talos Linux · plan and apply · a live web console

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Talos Linux](https://img.shields.io/badge/Talos%20Linux-v1.14-ff7300)
![Kubernetes](https://img.shields.io/badge/Kubernetes-v1.37-326ce5?logo=kubernetes&logoColor=white)
[![License](https://img.shields.io/badge/license-LGPL--2.1+-blue)](LICENSE)

[Documentation](docs/README.md) · [Discord](https://discord.gg/kNysBA9VU5)

<img src="docs/images/console.png" width="820" alt="The Kubit console showing a Talos Kubernetes cluster with its nodes and current plan" />

</div>

Got a few mini PCs, an old server or some VMs? Kubit turns them into a
[Talos Linux](https://www.talos.dev) Kubernetes cluster and keeps the whole setup in a git
repo you own.

You describe the cluster in one file. Kubit shows you a plan, and when you're happy, applies
it. Click through the web console or edit the file yourself. Both end up in the same place.

## Why Kubit

- **Your cluster is a folder in git.** No database, nothing hidden. Clone it on another
  laptop and you're back in business.
- **See before you change.** Every change starts as a plan you can review, like Terraform.
- **Bare metal friendly.** Boot a machine, and Kubit finds it on your network.
- **Safe upgrades.** Talos and Kubernetes upgrade one node at a time, with a backup first.
- **Batteries included.** Load balancer, ingress, certificates, storage and GitOps with Flux,
  one switch each.
- **No lock-in.** Plain `talosctl` and `kubectl` always work, with or without Kubit.

## Get started

You need Go 1.26+, Node 20+ and git, on macOS or Linux.

```sh
git clone https://github.com/mikaelhug/Kubit.git
cd Kubit
make build
bin/kubit
```

The console opens at http://127.0.0.1:8090. Then:

1. Boot your machines from the [Talos ISO](https://factory.talos.dev), or run `sudo bin/kubit pxe`
   to boot them over the network.
2. Open **Discovery**, select your machines, choose **Add** and pick a folder for the cluster.
3. Review the plan and choose **Apply**.

That's it, you have a cluster. Commit the new folder to git.

<div align="center">
<img src="docs/images/plan.png" width="820" alt="A Kubit plan: adding a worker and upgrading Talos, ready to apply" />
</div>

## Prefer the terminal?

```sh
kubit init ~/lab --nodes 192.168.1.0/24
kubit plan ~/lab
kubit apply ~/lab
```

Run `kubit --help` for the rest.

## Learn more

- [The cluster file](docs/cluster-yaml.md): every setting in `cluster.yaml`
- [Operations](docs/operations.md): upgrades, moving nodes, add-ons, backups
- [Console and API](docs/console.md): what the web console can do
- [Development](docs/development.md): building and hacking on Kubit

## License

Kubit is free software under the [LGPL-2.1-or-later](LICENSE).
