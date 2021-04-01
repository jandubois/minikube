/*
Copyright 2021 The Kubernetes Authors All rights reserved.

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

package bsutil

import (
	"bytes"
	"fmt"
	"path"

	"github.com/pkg/errors"
	"k8s.io/minikube/pkg/minikube/bootstrapper/bsutil/ktmpl"
	"k8s.io/minikube/pkg/minikube/config"
	"k8s.io/minikube/pkg/minikube/cruntime"
	"k8s.io/minikube/pkg/util"
)

var (
	K3s = "k3s"
)

func extraK3sOpts(mc config.ClusterConfig) (map[string]string, error) {
	k8s := mc.KubernetesConfig
	version, err := util.ParseKubernetesVersion(k8s.KubernetesVersion)
	if err != nil {
		return nil, errors.Wrap(err, "parsing Kubernetes version")
	}

	extraOpts, err := extraConfigForComponent(K3s, k8s.ExtraOptions, version)
	if err != nil {
		return nil, errors.Wrap(err, "generating extra configuration for k3s")
	}

	return extraOpts, nil
}

// NewK3sConfig generates a new systemd unit containing a configured k3s service
// based on the options present in the KubernetesConfig.
func NewK3sConfig(mc config.ClusterConfig, nc config.Node, r cruntime.Manager) ([]byte, error) {
	b := bytes.Buffer{}
	// nc not yet used
	extraOpts, err := extraK3sOpts(mc)
	if err != nil {
		return nil, err
	}

	// TODO(jandubois): Is there a better way to check the runtime type?
	if r.Name() == "Docker" {
		cgroupDriver, err := r.CGroupDriver()
		if err != nil {
			return nil, errors.Wrap(err, "getting cgroup driver")
		}
		extraOpts["docker"] = "true"
		extraOpts["kubelet-arg"] = fmt.Sprintf("cgroup-driver=%s", cgroupDriver)
	} else {
		// extraOpts["container-runtime-endpoint"] = r.SocketPath()
	}

	k8s := mc.KubernetesConfig
	opts := struct {
		ExtraOptions string
		K3sPath      string
	}{
		ExtraOptions: convertToFlags(extraOpts),
		K3sPath:      path.Join(binRoot(k8s.KubernetesVersion), "k3s"),
	}
	if err := ktmpl.K3sServiceTemplate.Execute(&b, opts); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
