/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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

// Package bsutil will eventually be renamed to kubeadm package after getting rid of older one
package bsutil

import (
	"bytes"
	"path"

	"github.com/pkg/errors"
	"k8s.io/minikube/pkg/minikube/bootstrapper/bsutil/ktmpl"
	"k8s.io/minikube/pkg/minikube/bootstrapper/images"
	"k8s.io/minikube/pkg/minikube/cni"
	"k8s.io/minikube/pkg/minikube/config"
	"k8s.io/minikube/pkg/minikube/cruntime"
	"k8s.io/minikube/pkg/util"
)

var (
	K3s = "k3s"
)

func extraK3sOpts(mc config.ClusterConfig, nc config.Node, r cruntime.Manager) (map[string]string, error) {
	k8s := mc.KubernetesConfig
	version, err := util.ParseKubernetesVersion(k8s.KubernetesVersion)
	if err != nil {
		return nil, errors.Wrap(err, "parsing Kubernetes version")
	}

	extraOpts, err := extraConfigForComponent(K3s, k8s.ExtraOptions, version)
	if err != nil {
		return nil, errors.Wrap(err, "generating extra configuration for kubelet")
	}

	//for k, v := range r.KubeletOptions() {
	//	extraOpts[k] = v
	//}
	if k8s.NetworkPlugin != "" {
		extraOpts["network-plugin"] = k8s.NetworkPlugin

		if k8s.NetworkPlugin == "kubenet" {
			extraOpts["pod-cidr"] = cni.DefaultPodCIDR
		}
	}

	if _, ok := extraOpts["node-ip"]; !ok {
		extraOpts["node-ip"] = nc.IP
	}
	//if _, ok := extraOpts["hostname-override"]; !ok {
	//	nodeName := KubeNodeName(mc, nc)
	//	extraOpts["hostname-override"] = nodeName
	//}

	pauseImage := images.Pause(version, k8s.ImageRepository)
	if _, ok := extraOpts["pod-infra-container-image"]; !ok && k8s.ImageRepository != "" && pauseImage != "" && k8s.ContainerRuntime != remoteContainerRuntime {
		extraOpts["pod-infra-container-image"] = pauseImage
	}

	// parses a map of the feature gates for kubelet
	_, kubeletFeatureArgs, err := parseFeatureArgs(k8s.FeatureGates)
	if err != nil {
		return nil, errors.Wrap(err, "parses feature gate config for kubelet")
	}

	if kubeletFeatureArgs != "" {
		extraOpts["feature-gates"] = kubeletFeatureArgs
	}

	return extraOpts, nil
}

// NewK3sConfig generates a new systemd unit containing a configured k3s service
// based on the options present in the KubernetesConfig.
func NewK3sConfig(mc config.ClusterConfig, nc config.Node, r cruntime.Manager) ([]byte, error) {
	b := bytes.Buffer{}
	extraOpts, err := extraK3sOpts(mc, nc, r)
	if err != nil {
		return nil, err
	}
	k8s := mc.KubernetesConfig
	opts := struct {
		ExtraOptions     string
		ContainerRuntime string
		K3sPath          string
	}{
		ExtraOptions:     convertToFlags(extraOpts),
		ContainerRuntime: k8s.ContainerRuntime,
		K3sPath:          path.Join(binRoot(k8s.KubernetesVersion), "k3s"),
	}
	if err := ktmpl.K3sServiceTemplate.Execute(&b, opts); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
