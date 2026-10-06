package cluster

import "fmt"

func kubeconfigFor(server string) []byte {
	return fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: %s
contexts:
- name: c
  context:
    cluster: c
    user: u
current-context: c
users:
- name: u
  user:
    token: t
`, server)
}
