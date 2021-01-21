# Add k3s as an alternate bootstrapper

* First proposed: 2021-01-20
* Authors: Jan Dubois (@jandubois)

## Reviewer Priorities

Please review this proposal with the following priorities:

*   Does this fit with minikube's [principles](https://minikube.sigs.k8s.io/docs/concepts/principles/)?
*   Are there other approaches to consider?
*   Could the implementation be made simpler?
*   Are there usability, reliability, or technical debt concerns?

## Summary

[K3s](https://k3s.io/) is a CNCF sandbox project for bootstrapping a lightweight
Kubernetes setup on developer machines or resource constrained "edge" servers
(e.g. Raspberry Pi). Supporting k3s as an alternative to kubeadm should provide
faster startup times and less resource usage on developer machines as well as
a development setup that closely matches the final deployment configuration
(for developers targeting k3s).

## Goals

*   Provide the same functionaly (using the same interface) as with kubeadm.
*   Minimize resource usage (CPU, RAM, disk "sync" calls).
*   Use the kubernetes binaries bundled in the k3s executable.

## Non-Goals

*   Support production deployments.
*   Support development of k3s itself.

## Design Details

minikube already has a `--bootstrapper` option, with `kubeadm` as the only
supported implementation. A new `k3s` choice would be used with a corresponding
implementation of the bootstrapper interface to download the k3s binary and
the k3s-airgap-images from Github and use them to provision kubernetes inside
the node.

k3s embeds kubelet, apiserver, scheduler, and controller manager and runs them
all inside a single process, so the existing bootstrapper interface will need
to be generalized to abstract the differences between kubeadm and k3s.

Testing should be done by running integration tests with the `k3s`
bootstrapper, i.e. after running `minikube config set bootstrapper k3s`.

## Alternatives Considered

An existing alternative is [k3d](https://k3d.io/), which is like
[kind](https://kind.sigs.k8s.io/) for k3s. The disadvantage is that
it requires Docker Desktop, which uses significant resources and
is also only partially open-source.
