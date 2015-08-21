/*
Copyright 2014 The Kubernetes Authors All rights reserved.

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

package client

import (
	"time"
)

// FlagSet abstracts the flag interface for compatibility with both Golang "flag"
// and cobra pflags (Posix style).
type FlagSet interface {
	StringVar(p *string, name, value, usage string)
	BoolVar(p *bool, name string, value bool, usage string)
	UintVar(p *uint, name string, value uint, usage string)
	DurationVar(p *time.Duration, name string, value time.Duration, usage string)
	IntVar(p *int, name string, value int, usage string)
}

func BindKubeletClientConfigFlags(flags FlagSet, config *KubeletConfig) {
	flags.BoolVar(&config.EnableHttps, "kubelet-https", config.EnableHttps, "Use https for kubelet connections")
	flags.UintVar(&config.Port, "kubelet-port", config.Port, "Kubelet port")
	flags.DurationVar(&config.HTTPTimeout, "kubelet-timeout", config.HTTPTimeout, "Timeout for kubelet operations")
	flags.StringVar(&config.CertFile, "kubelet-client-certificate", config.CertFile, "Path to a client key file for TLS.")
	flags.StringVar(&config.KeyFile, "kubelet-client-key", config.KeyFile, "Path to a client key file for TLS.")
	flags.StringVar(&config.CAFile, "kubelet-certificate-authority", config.CAFile, "Path to a cert. file for the certificate authority.")
}
