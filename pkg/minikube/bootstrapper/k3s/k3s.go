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
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"time"

	"github.com/docker/machine/libmachine"
	"github.com/docker/machine/libmachine/state"
	"github.com/pkg/errors"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/minikube/pkg/minikube/assets"
	"k8s.io/minikube/pkg/minikube/bootstrapper"
	"k8s.io/minikube/pkg/minikube/bootstrapper/bsutil"
	"k8s.io/minikube/pkg/minikube/bootstrapper/bsutil/kverify"
	"k8s.io/minikube/pkg/minikube/command"
	"k8s.io/minikube/pkg/minikube/config"
	"k8s.io/minikube/pkg/minikube/cruntime"
	"k8s.io/minikube/pkg/minikube/localpath"
	"k8s.io/minikube/pkg/minikube/sysinit"
	"k8s.io/minikube/pkg/minikube/vmpath"
	"k8s.io/minikube/pkg/util/lock"
)

// Bootstrapper is a bootstrapper using k3s
type Bootstrapper struct {
	c           command.Runner
	k8sClient   *kubernetes.Clientset // Kubernetes client used to verify pods inside cluster
	contextName string
}

// NewBootstrapper creates a new k3s.Bootstrapper
func NewBootstrapper(api libmachine.API, cc config.ClusterConfig, r command.Runner) (*Bootstrapper, error) {
	return &Bootstrapper{c: r, contextName: cc.Name, k8sClient: nil}, nil
}

// GetAPIServerStatus returns the api-server status
func (k *Bootstrapper) GetAPIServerStatus(hostname string, port int) (string, error) {
	s, err := kverify.APIServerStatus(k.c, bootstrapper.K3s, hostname, port)
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
	start := time.Now()
	klog.Infof("StartCluster: %+v", cfg)
	defer func() {
		klog.Infof("StartCluster complete in %s", time.Since(start))
	}()

	if err := sysinit.New(k.c).Restart("k3s"); err != nil {
		klog.Warningf("Failed to restart k3s: %v", err)
		// TODO(jandubois): Why don't we return an error (the kubeadm bootstrapper doesn't either)?
	}
	// Once "systemctl start k3s" returns with a 0 exit code the apiserver is functional
	// (but pods will still be starting up).

	// At this point k3s will have overwritten the /var/lib/minikube/kubeconfig file
	// with its own version that includes the correct client certs.

	// Since there is no way to make k3s use pre-generated client certs the new certs have
	// to be copied back to the local machine to make that kubeconfig functional as well.

	// copy [vm]/var/lib/k3s/server/tls/client-admin.* → [local]$MINIKUBE_HOME/profiles/$NAME/client.*
	for _, ext := range []string{".crt", ".key"} {
		src := path.Join(vmpath.GuestK3sServerCertsDir, "client-admin"+ext)
		rr, err := k.c.RunCmd(exec.Command("sudo", "cat", src))
		if err != nil {
			klog.Infof("remote cat failed: %v", err)
		}

		perm := os.FileMode(0644)
		if ext == ".key" {
			perm = os.FileMode(0600)
		}
		dest := filepath.Join(localpath.Profile(cfg.Name), "client"+ext)
		err = lock.WriteFile(dest, rr.Stdout.Bytes(), perm)
		if err != nil {
			return errors.Wrapf(err, "Error writing file %s", dest)
		}
	}
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
	// The only reason this method implementation isn't empty is to make k3s use
	// the shared minikube server CA. k3s will manage all certs on its own, but
	// would create a separate server CA for each cluster (profile).

	// TODO(jandubois): Don't generate client certs; k3s can't use them. Maybe not worth bothering though.
	if _, err := bootstrapper.SetupCerts(k.c, k8s, n); err != nil {
		return err
	}

	// Copy the cluster CA cert&key to the k3s certs directory, so it doesn't create its own.
	if _, err := k.c.RunCmd(exec.Command("sudo", "mkdir", "-p", vmpath.GuestK3sServerCertsDir)); err != nil {
		return errors.Wrap(err, "mkdir k3s tls dir")
	}

	// copy /var/lib/minikube/certs/ca.* → /var/lib/k3s/server/tls/server-ca.*
	for _, ext := range []string{".crt", ".key"} {
		src := path.Join(vmpath.GuestPersistentDir, "certs", "ca"+ext)
		dest := path.Join(vmpath.GuestK3sServerCertsDir, "server-ca"+ext)
		if _, err := k.c.RunCmd(exec.Command("sudo", "cp", src, dest)); err != nil {
			return errors.Wrap(err, "copy server certs")
		}
	}

	// k3s breaks when a pre-generated server CA cert is added to the system trust store:
	// https://github.com/k3s-io/k3s/issues/2731
	// TODO(jandubois): Wrap the workaround in a k8s version check once there is a fix
	minikubeCA := path.Join(vmpath.GuestCertAuthDir, "minikubeCA.pem")
	if _, err := k.c.RunCmd(exec.Command("sudo", "rm", minikubeCA)); err != nil {
		return errors.Wrap(err, "remove minikubeCA")
	}

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
	binPath := path.Join(vmpath.GuestPersistentDir, "binaries", cfg.KubernetesConfig.KubernetesVersion)
	k3sPath := path.Join(binPath, "k3s")
	c := exec.Command("sudo", "ln", "-s", k3sPath, kubectlPath(cfg))
	if rr, err := k.c.RunCmd(c); err != nil {
		return errors.Wrapf(err, "create symlink failed: %s", rr.Command())
	}

	// Create symlink for tarball of preload images
	if _, err := k.c.RunCmd(exec.Command("sudo", "mkdir", "-p", vmpath.GuestK3sImagesDir)); err != nil {
		return errors.Wrap(err, "mkdir k3s images dir")
	}

	preloadPath := path.Join(vmpath.GuestK3sImagesDir, "preload.tar")
	c = exec.Command("sudo", "ln", "-s", path.Join(binPath, "k3s-airgap-images"), preloadPath)
	if rr, err := k.c.RunCmd(c); err != nil {
		return errors.Wrapf(err, "create symlink failed: %s", rr.Command())
	}

	k3sCfg, err := bsutil.NewK3sConfig(cfg, n)
	if err != nil {
		return errors.Wrap(err, "generating k3s config")
	}

	klog.Infof("k3s %s config:\n%+v", k3sCfg, cfg.KubernetesConfig)

	files := []assets.CopyableFile{
		assets.NewMemoryAssetTarget(k3sCfg, bsutil.K3sSystemdConfFile, "0644"),
	}

	// Installs compatibility shims for non-systemd environments
	shims, err := sm.GenerateInitShim("k3s", k3sPath, bsutil.K3sSystemdConfFile)
	if err != nil {
		return errors.Wrap(err, "shim")
	}
	files = append(files, shims...)

	if err := bsutil.CopyFiles(k.c, files); err != nil {
		return errors.Wrap(err, "copy")
	}

	return nil
}

// kubectlPath returns the path to the kubectl command
func kubectlPath(cfg config.ClusterConfig) string {
	return path.Join(vmpath.GuestPersistentDir, "binaries", cfg.KubernetesConfig.KubernetesVersion, "kubectl")
}
