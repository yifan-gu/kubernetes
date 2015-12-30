/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package rkt

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
)

const (
	defaultNetConfigFile = "k8s-cbr0.conf"
	defaultNetworkName   = "rkt.kubernetes.io"
)

const NET_CONFIG_TEMPLATE = `{
  "cniVersion": "0.1.0",
  "name": "%s",
  "type": "bridge",
  "bridge": "%s",
  "mtu": %d,
  "addIf": "%s",
  "isGateway": true,
  "ipMasq": true,
  "ipam": {
    "type": "host-local",
    "subnet": "%s",
    "gateway": "%s",
    "routes": [
      { "dst": "0.0.0.0/0" }
    ]
  }
}`

// WriteBridgeNetConfig creates and write the CNI bridge configure file at ${rktLocalConfigDir}/net.d/${defaultNetConfigFile}.
// bridgeName is the name of the container bridge, e.g. 'cbr0'.
// cidr is the CIDR block of the bridge, note that cidr.IP is the gateway of the bridge.
func WriteBridgeNetConfig(bridgeName string, cidr *net.IPNet) error {
	data := fmt.Sprintf(NET_CONFIG_TEMPLATE, defaultNetworkName, "cbr0", 1460, "eth0", cidr.String(), cidr.IP.String())

	// Ensure the 'net.d' dir exists.
	dirpath := path.Join(rktLocalConfigDir, "net.d")
	err := os.MkdirAll(dirpath, 0750)
	if err != nil && !os.IsExist(err) {
		return err
	}

	return ioutil.WriteFile(path.Join(dirpath, defaultNetConfigFile), []byte(data), 0640)
}
