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

package download

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	"k8s.io/klog/v2"
	"k8s.io/minikube/pkg/minikube/localpath"
)

// binaryWithChecksumURL gets the location of a Kubernetes binary
func binaryWithChecksumURL(binaryName, version, osName, archName string) (string, error) {
	if strings.HasPrefix(binaryName, "k3s") {
		suffix := "-" + archName
		switch binaryName {
		case "k3s":
			switch archName {
			case "amd64":
				suffix = ""
			case "arm":
				suffix = "-armhf"
			}
		case "k3s-airgap-images":
			suffix += ".tar"
		}
		// version must be encoded because it contains a '+' char, e.g. "v1.20.0+k3s2"
		base := fmt.Sprintf("https://github.com/k3s-io/k3s/releases/download/%s", url.QueryEscape(version))
		return fmt.Sprintf("%s/%s?checksum=file:%s/sha256sum-%s.txt", base, binaryName+suffix, base, archName), nil
	}

	base := fmt.Sprintf("https://storage.googleapis.com/kubernetes-release/release/%s/bin/%s/%s/%s", version, osName, archName, binaryName)
	v, err := semver.Make(version[1:])
	if err != nil {
		return "", err
	}

	if v.GTE(semver.MustParse("1.17.0")) {
		return fmt.Sprintf("%s?checksum=file:%s.sha256", base, base), nil
	}
	return fmt.Sprintf("%s?checksum=file:%s.sha1", base, base), nil
}

// Binary will download a binary onto the host
func Binary(binary, version, osName, archName string) (string, error) {
	targetDir := localpath.MakeMiniPath("cache", osName, version)
	targetFilepath := path.Join(targetDir, binary)

	url, err := binaryWithChecksumURL(binary, version, osName, archName)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(targetFilepath); err == nil {
		klog.Infof("Not caching binary, using %s", url)
		return targetFilepath, nil
	}

	if err := download(url, targetFilepath); err != nil {
		return "", errors.Wrapf(err, "download failed: %s", url)
	}

	if osName == runtime.GOOS && archName == runtime.GOARCH {
		if err = os.Chmod(targetFilepath, 0755); err != nil {
			return "", errors.Wrapf(err, "chmod +x %s", targetFilepath)
		}
	}
	return targetFilepath, nil
}
