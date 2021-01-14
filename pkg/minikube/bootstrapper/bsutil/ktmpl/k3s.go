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

package ktmpl

import "text/template"

// K3sServiceTemplate is the base k3s systemd template, written to k3sServiceFile
var K3sServiceTemplate = template.Must(template.New("k3sServiceTemplate").Parse(`[Unit]
{{- if or (eq .ContainerRuntime "cri-o") (eq .ContainerRuntime "cri") }}
Wants=crio.service
{{- else if eq .ContainerRuntime "containerd" }}
Wants=containerd.service
{{- else }}
Wants=docker.socket
{{- end }}
Description=Lightweight Kubernetes
Documentation=https://k3s.io
Wants=network-online.target
After=network-online.target

[Install]
WantedBy=multi-user.target

[Service]
Type=notify
# EnvironmentFile=${FILE_K3S_ENV}
KillMode=process
Delegate=yes
# Having non-zero Limit*s causes performance problems due to accounting overhead
# in the kernel. We recommend using cgroups to do container-local accounting.
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
Restart=always
RestartSec=5s
ExecStartPre=-/sbin/modprobe br_netfilter
ExecStartPre=-/sbin/modprobe overlay
ExecStart={{.K3sPath}} server{{if .ExtraOptions}} {{.ExtraOptions}}{{end}}
`))
