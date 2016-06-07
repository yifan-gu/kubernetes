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

package service

import (
	"testing"

	"k8s.io/kubernetes/federation/apis/federation"
	"k8s.io/kubernetes/federation/pkg/dnsprovider/providers/google/clouddns" // Only for unit testing purposes.
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util/sets"
)

var fakeDns, _ = clouddns.NewFakeInterface() // No need to check for unsupported interfaces, as the fake interface supports everything that's required.
var fakeDnsZones, _ = fakeDns.Zones()

var fakeServiceController = ServiceController{
	dns:          fakeDns,
	dnsZones:     fakeDnsZones,
	serviceCache: &serviceCache{fedServiceMap: make(map[string]*cachedService)},
	clusterCache: &clusterClientCache{
		clientMap: make(map[string]*clusterCache),
	},
	knownClusterSet: make(sets.String),
}

func buildEndpoint(subsets [][]string) *api.Endpoints {
	endpoint := &api.Endpoints{
		Subsets: []api.EndpointSubset{
			{Addresses: []api.EndpointAddress{}},
		},
	}
	for _, element := range subsets {
		address := api.EndpointAddress{IP: element[0], Hostname: element[1], TargetRef: nil}
		endpoint.Subsets[0].Addresses = append(endpoint.Subsets[0].Addresses, address)
	}
	return endpoint
}

func TestProcessEndpointUpdate(t *testing.T) {
	cc := clusterClientCache{
		clientMap: make(map[string]*clusterCache),
	}
	tests := []struct {
		name          string
		cachedService *cachedService
		endpoint      *api.Endpoints
		clusterName   string
		expectResult  int
	}{
		{
			"no-cache",
			&cachedService{
				lastState:   &api.Service{},
				endpointMap: make(map[string]int),
			},
			buildEndpoint([][]string{{"ip1", ""}}),
			"foo",
			1,
		},
		{
			"has-cache",
			&cachedService{
				lastState: &api.Service{},
				endpointMap: map[string]int{
					"foo": 1,
				},
			},
			buildEndpoint([][]string{{"ip1", ""}}),
			"foo",
			1,
		},
	}

	for _, test := range tests {
		cc.processEndpointUpdate(test.cachedService, test.endpoint, test.clusterName, &fakeServiceController)
		if test.expectResult != test.cachedService.endpointMap[test.clusterName] {
			t.Errorf("Test failed for %s, expected %v, saw %v", test.name, test.expectResult, test.cachedService.endpointMap[test.clusterName])
		}
	}
}

func TestProcessEndpointDeletion(t *testing.T) {
	clusterName := "foo"
	cc := clusterClientCache{
		clientMap: map[string]*clusterCache{
			clusterName: {
				cluster: &federation.Cluster{
					Status: federation.ClusterStatus{
						Zones:  []string{"foozone"},
						Region: "fooregion",
					},
				},
			},
		},
	}
	tests := []struct {
		name          string
		cachedService *cachedService
		endpoint      *api.Endpoints
		clusterName   string
		expectResult  int
	}{
		{
			"no-cache",
			&cachedService{
				lastState:   &api.Service{},
				endpointMap: make(map[string]int),
			},
			buildEndpoint([][]string{{"ip1", ""}}),
			clusterName,
			0,
		},
		{
			"has-cache",
			&cachedService{
				lastState: &api.Service{},
				endpointMap: map[string]int{
					clusterName: 1,
				},
			},
			buildEndpoint([][]string{{"ip1", ""}}),
			clusterName,
			0,
		},
	}
	fakeServiceController.clusterCache = &cc
	for _, test := range tests {
		cc.processEndpointDeletion(test.cachedService, test.clusterName, &fakeServiceController)
		if test.expectResult != test.cachedService.endpointMap[test.clusterName] {
			t.Errorf("Test failed for %s, expected %v, saw %v", test.name, test.expectResult, test.cachedService.endpointMap[test.clusterName])
		}
	}
}
