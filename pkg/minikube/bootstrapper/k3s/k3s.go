/*
Copyright 2020 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package k3s

import (
	"os/exec"
	"path"
	"time"

	"github.com/docker/machine/libmachine"
	"github.com/docker/machine/libmachine/state"
	"github.com/pkg/errors"

	"k8s.io/client-go/kubernetes"
	"k8s.io/minikube/pkg/minikube/bootstrapper"
	"k8s.io/minikube/pkg/minikube/bootstrapper/bsutil"
	"k8s.io/minikube/pkg/minikube/bootstrapper/bsutil/kverify"
	"k8s.io/minikube/pkg/minikube/command"
	"k8s.io/minikube/pkg/minikube/config"
	"k8s.io/minikube/pkg/minikube/cruntime"
	"k8s.io/minikube/pkg/minikube/sysinit"
	"k8s.io/minikube/pkg/minikube/vmpath"
)

// Bootstrapper is a bootstrapper using kubeadm
type Bootstrapper struct {
	c           command.Runner
	k8sClient   *kubernetes.Clientset // Kubernetes client used to verify pods inside cluster
	contextName string
}

// NewBootstrapper creates a new kubeadm.Bootstrapper
func NewBootstrapper(api libmachine.API, cc config.ClusterConfig, r command.Runner) (*Bootstrapper, error) {
	return &Bootstrapper{c: r, contextName: cc.Name, k8sClient: nil}, nil
}

// GetAPIServerStatus returns the api-server status
func (k *Bootstrapper) GetAPIServerStatus(hostname string, port int) (string, error) {
	s, err := kverify.APIServerStatus(k.c, hostname, port)
	if err != nil {
		return state.Error.String(), err
	}
	return s.String(), nil
}

// LogCommands returns a map of log type to a command which will display that log.
func (k *Bootstrapper) LogCommands(cfg config.ClusterConfig, o bootstrapper.LogOptions) map[string]string {
	return map[string]string{}
}

// StartCluster starts the cluster
func (k *Bootstrapper) StartCluster(cfg config.ClusterConfig) error {
	return nil
}

// WaitForNode blocks until the node appears to be healthy
func (k *Bootstrapper) WaitForNode(cfg config.ClusterConfig, n config.Node, timeout time.Duration) error {
	return nil
}

// JoinCluster adds a node to an existing cluster
func (k *Bootstrapper) JoinCluster(cc config.ClusterConfig, n config.Node, joinCmd string) error {
	return nil
}

// GenerateToken creates a token and returns the appropriate kubeadm join command to run, or the already existing token
func (k *Bootstrapper) GenerateToken(cc config.ClusterConfig) (string, error) {
	return "", nil
}

// DeleteCluster removes the components that were started earlier
func (k *Bootstrapper) DeleteCluster(k8s config.KubernetesConfig) error {
	return nil
}

// SetupCerts sets up certificates within the cluster.
func (k *Bootstrapper) SetupCerts(k8s config.KubernetesConfig, n config.Node) error {
	return nil
}

// UpdateCluster updates the control plane with cluster-level info.
func (k *Bootstrapper) UpdateCluster(cfg config.ClusterConfig) error {
	cp, err := config.PrimaryControlPlane(&cfg)
	if err != nil {
		return errors.Wrap(err, "getting control plane")
	}

	err = k.UpdateNode(cfg, cp, nil)
	if err != nil {
		return errors.Wrap(err, "updating control plane")
	}

	return nil
}

// UpdateNode updates a node.
func (k *Bootstrapper) UpdateNode(cfg config.ClusterConfig, n config.Node, r cruntime.Manager) error {
	sm := sysinit.New(k.c)

	if err := bsutil.TransferBinaries(cfg.KubernetesConfig, cfg.Bootstrapper, k.c, sm); err != nil {
		return errors.Wrap(err, "downloading binaries")
	}

	// Create kubectl symlink to k3s
	k3sPath := path.Join(vmpath.GuestPersistentDir, "binaries", cfg.KubernetesConfig.KubernetesVersion, "k3s")
	c := exec.Command("sudo", "ln", "-s", k3sPath, kubectlPath(cfg))
	if rr, err := k.c.RunCmd(c); err != nil {
		return errors.Wrapf(err, "create symlink failed: %s", rr.Command())
	}

	return nil
}

// kubectlPath returns the path to the kubectl command
func kubectlPath(cfg config.ClusterConfig) string {
	return path.Join(vmpath.GuestPersistentDir, "binaries", cfg.KubernetesConfig.KubernetesVersion, "kubectl")
}
