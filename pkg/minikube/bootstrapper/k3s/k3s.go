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
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"k8s.io/minikube/pkg/drivers/kic/oci"
	"k8s.io/minikube/pkg/kapi"
	"k8s.io/minikube/pkg/minikube/driver"
	"k8s.io/minikube/pkg/minikube/out"
	"k8s.io/minikube/pkg/minikube/out/register"
	"k8s.io/minikube/pkg/minikube/style"

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
	"k8s.io/minikube/pkg/minikube/constants"
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

	// Note the the "k3s" service is called "kubelet" inside minikube.
	if err := sysinit.New(k.c).Restart("kubelet"); err != nil {
		klog.Warningf("Failed to restart kubelet (k3s): %v", err)
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

// client sets and returns a Kubernetes client to use to speak to a kubeadm launched apiserver
func (k *Bootstrapper) client(ip string, port int) (*kubernetes.Clientset, error) {
	if k.k8sClient != nil {
		return k.k8sClient, nil
	}

	cc, err := kapi.ClientConfig(k.contextName)
	if err != nil {
		return nil, errors.Wrap(err, "client config")
	}

	endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(ip, strconv.Itoa(port)))
	if cc.Host != endpoint {
		klog.Warningf("Overriding stale ClientConfig host %s with %s", cc.Host, endpoint)
		cc.Host = endpoint
	}
	c, err := kubernetes.NewForConfig(cc)
	if err == nil {
		k.k8sClient = c
	}
	return c, err
}

// WaitForNode blocks until the node appears to be healthy
func (k *Bootstrapper) WaitForNode(cfg config.ClusterConfig, n config.Node, timeout time.Duration) error {
	start := time.Now()
	register.Reg.SetStep(register.VerifyingKubernetes)
	out.Step(style.HealthCheck, "Verifying Kubernetes components...")
	// regardless if waiting is set or not, we will make sure kubelet is not stopped
	// to solve corner cases when a container is hibernated and once coming back kubelet not running.
	if err := k.ensureServiceStarted("kubelet"); err != nil {
		klog.Warningf("Couldn't ensure kubelet is started this might cause issues: %v", err)
	}
	// TODO: #7706: for better performance we could use k.client inside minikube to avoid asking for external IP:PORT
	cp, err := config.PrimaryControlPlane(&cfg)
	if err != nil {
		return errors.Wrap(err, "get primary control plane")
	}
	hostname, _, port, err := driver.ControlPlaneEndpoint(&cfg, &cp, cfg.Driver)
	if err != nil {
		return errors.Wrap(err, "get control plane endpoint")
	}

	client, err := k.client(hostname, port)
	if err != nil {
		return errors.Wrap(err, "kubernetes client")
	}

	if !kverify.ShouldWait(cfg.VerifyComponents) {
		klog.Infof("skip waiting for components based on config.")

		if err := kverify.NodePressure(client); err != nil {
			adviseNodePressure(err, cfg.Name, cfg.Driver)
			return errors.Wrap(err, "node pressure")
		}
		return nil
	}

	cr, err := cruntime.New(cruntime.Config{Type: cfg.KubernetesConfig.ContainerRuntime, Runner: k.c})
	if err != nil {
		return errors.Wrapf(err, "create runtme-manager %s", cfg.KubernetesConfig.ContainerRuntime)
	}

	if n.ControlPlane {
		if cfg.VerifyComponents[kverify.APIServerWaitKey] {
			if err := kverify.WaitForAPIServerProcess(cr, k, cfg, k.c, start, timeout); err != nil {
				return errors.Wrap(err, "wait for apiserver proc")
			}

			if err := kverify.WaitForHealthyAPIServer(cr, k, cfg, k.c, client, start, hostname, port, timeout); err != nil {
				return errors.Wrap(err, "wait for healthy API server")
			}
		}

		if cfg.VerifyComponents[kverify.SystemPodsWaitKey] {
			if err := kverify.WaitForSystemPods(cr, k, cfg, k.c, client, start, timeout); err != nil {
				return errors.Wrap(err, "waiting for system pods")
			}
		}

		if cfg.VerifyComponents[kverify.DefaultSAWaitKey] {
			if err := kverify.WaitForDefaultSA(client, timeout); err != nil {
				return errors.Wrap(err, "waiting for default service account")
			}
		}

		if cfg.VerifyComponents[kverify.AppsRunningKey] {
			if err := kverify.WaitForAppsRunning(client, kverify.AppsRunningList, timeout); err != nil {
				return errors.Wrap(err, "waiting for apps_running")
			}
		}
	}
	if cfg.VerifyComponents[kverify.KubeletKey] {
		if err := kverify.WaitForService(k.c, "kubelet", timeout); err != nil {
			return errors.Wrap(err, "waiting for kubelet")
		}

	}

	if cfg.VerifyComponents[kverify.NodeReadyKey] {
		if err := kverify.WaitForNodeReady(client, timeout); err != nil {
			return errors.Wrap(err, "waiting for node to be ready")
		}
	}

	klog.Infof("duration metric: took %s to wait for : %+v ...", time.Since(start), cfg.VerifyComponents)

	if err := kverify.NodePressure(client); err != nil {
		adviseNodePressure(err, cfg.Name, cfg.Driver)
		return errors.Wrap(err, "node pressure")
	}
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
	r, err := cruntime.New(cruntime.Config{
		Type:   cfg.KubernetesConfig.ContainerRuntime,
		Runner: k.c,
		Socket: cfg.KubernetesConfig.CRISocket,
	})
	if err != nil {
		return errors.Wrap(err, "runtime")
	}

	cp, err := config.PrimaryControlPlane(&cfg)
	if err != nil {
		return errors.Wrap(err, "getting control plane")
	}

	err = k.UpdateNode(cfg, cp, r)
	if err != nil {
		return errors.Wrap(err, "updating control plane")
	}

	return nil
}

// TODO(jandubois): Move this to a shared location somewhere in bsutils
// CreateSymlink creates a symlink (if the destination doesn't already exist)
func CreateSymlink(r command.Runner, source string, dest string) error {
	// TODO(jandubois): should we use https://github.com/alessio/shellescape to quote source/dest?
	shellCmd := fmt.Sprintf("test -e %s || ln -s %s %s", dest, source, dest)
	c := exec.Command("sudo", "/bin/sh", "-c", shellCmd)
	if rr, err := r.RunCmd(c); err != nil {
		return errors.Wrapf(err, "create symlink failed: %s", rr.Command())
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
	if err := CreateSymlink(k.c, k3sPath, kubectlPath(cfg)); err != nil {
		return err
	}

	// Create symlink for tarball of preload images
	if _, err := k.c.RunCmd(exec.Command("sudo", "mkdir", "-p", vmpath.GuestK3sImagesDir)); err != nil {
		return errors.Wrap(err, "mkdir k3s images dir")
	}

	preloadPath := path.Join(vmpath.GuestK3sImagesDir, "preload.tar")
	if err := CreateSymlink(k.c, path.Join(binPath, constants.K3sImagesTarball), preloadPath); err != nil {
		return err
	}

	k3sCfg, err := bsutil.NewK3sConfig(cfg, n, r)
	if err != nil {
		return errors.Wrap(err, "generating k3s config")
	}

	klog.Infof("k3s %s config:\n%+v", k3sCfg, cfg.KubernetesConfig)

	files := []assets.CopyableFile{
		assets.NewMemoryAssetTarget(k3sCfg, bsutil.KubeletServiceFile, "0644"),
	}

	// Installs compatibility shims for non-systemd environments
	shims, err := sm.GenerateInitShim("kubelet", k3sPath, bsutil.KubeletServiceFile)
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

// ensureKubeletStarted will start a systemd or init.d service if it is not running.
func (k *Bootstrapper) ensureServiceStarted(svc string) error {
	if st := kverify.ServiceStatus(k.c, svc); st != state.Running {
		klog.Warningf("surprisingly %q service status was %s!. will try to start it, could be related to this issue https://github.com/kubernetes/minikube/issues/9458", svc, st)
		return sysinit.New(k.c).Start(svc)
	}
	return nil
}

// adviseNodePressure will advise the user what to do with difference pressure errors based on their environment
func adviseNodePressure(err error, name string, drv string) {
	if diskErr, ok := err.(*kverify.ErrDiskPressure); ok {
		out.ErrLn("")
		klog.Warning(diskErr)
		out.WarningT("The node {{.name}} has ran out of disk space.", out.V{"name": name})
		// generic advice for all drivers
		out.Step(style.Tip, "Please free up disk or prune images.")
		if driver.IsVM(drv) {
			out.Step(style.Stopped, "Please create a cluster with bigger disk size: `minikube start --disk SIZE_MB` ")
		} else if drv == oci.Docker && runtime.GOOS != "linux" {
			out.Step(style.Stopped, "Please increse Desktop's disk size.")
			if runtime.GOOS == "darwin" {
				out.Step(style.Documentation, "Documentation: {{.url}}", out.V{"url": "https://docs.docker.com/docker-for-mac/space/"})
			}
			if runtime.GOOS == "windows" {
				out.Step(style.Documentation, "Documentation: {{.url}}", out.V{"url": "https://docs.docker.com/docker-for-windows/"})
			}
		}
		out.ErrLn("")
		return
	}

	if memErr, ok := err.(*kverify.ErrMemoryPressure); ok {
		out.ErrLn("")
		klog.Warning(memErr)
		out.WarningT("The node {{.name}} has ran out of memory.", out.V{"name": name})
		out.Step(style.Tip, "Check if you have unnecessary pods running by running 'kubectl get po -A")
		if driver.IsVM(drv) {
			out.Step(style.Stopped, "Consider creating a cluster with larger memory size using `minikube start --memory SIZE_MB` ")
		} else if drv == oci.Docker && runtime.GOOS != "linux" {
			out.Step(style.Stopped, "Consider increasing Docker Desktop's memory size.")
			if runtime.GOOS == "darwin" {
				out.Step(style.Documentation, "Documentation: {{.url}}", out.V{"url": "https://docs.docker.com/docker-for-mac/space/"})
			}
			if runtime.GOOS == "windows" {
				out.Step(style.Documentation, "Documentation: {{.url}}", out.V{"url": "https://docs.docker.com/docker-for-windows/"})
			}
		}
		out.ErrLn("")
		return
	}

	if pidErr, ok := err.(*kverify.ErrPIDPressure); ok {
		klog.Warning(pidErr)
		out.ErrLn("")
		out.WarningT("The node {{.name}} has ran out of available PIDs.", out.V{"name": name})
		out.ErrLn("")
		return
	}

	if netErr, ok := err.(*kverify.ErrNetworkNotReady); ok {
		klog.Warning(netErr)
		out.ErrLn("")
		out.WarningT("The node {{.name}} network is not available. Please verify network settings.", out.V{"name": name})
		out.ErrLn("")
		return
	}
}
