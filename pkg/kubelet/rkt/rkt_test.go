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
	"testing"

	rktapi "github.com/coreos/rkt/api/v1alpha"
	"github.com/stretchr/testify/assert"
)

func newTestRktRuntime() *Runtime {
	return &Runtime{
		apisvc: newFakeRktInterface(),
	}
}

// TODO(yifan): Add tests for systemd version testing.
// Plan here is to extract the systemd logic into a package to make it
// easier to mock the interface. See https://github.com/coreos/rkt/issues/1769.
func TestCheckVersion(t *testing.T) {
	fr := newFakeRktInterface()
	r := &Runtime{apisvc: fr}

	fr.info = rktapi.Info{
		RktVersion:  "1.2.3+git",
		AppcVersion: "1.2.4+git",
		ApiVersion:  "1.2.6-alpha",
	}
	tests := []struct {
		minimumRktBinVersion   string
		recommendRktBinVersion string
		minimumAppcVersion     string
		minimumRktApiVersion   string
		err                    error
	}{
		// Good versions.
		{
			"1.2.3",
			"1.2.3",
			"1.2.4",
			"1.2.5",
			nil,
		},
		// Good versions.
		{
			"1.2.3",
			"1.2.3",
			"1.2.4",
			"1.2.6-alpha",
			nil,
		},
		// Requires greater binary version.
		{
			"1.2.4",
			"1.2.4",
			"1.2.4",
			"1.2.6-alpha",
			fmt.Errorf("rkt: binary version is too old(%v), requires at least %v", fr.info.RktVersion, "1.2.4"),
		},
		// Requires greater Appc version.
		{
			"1.2.3",
			"1.2.3",
			"1.2.5",
			"1.2.6-alpha",
			fmt.Errorf("rkt: Appc version is too old(%v), requires at least %v", fr.info.AppcVersion, "1.2.5"),
		},
		// Requires greater API version.
		{
			"1.2.3",
			"1.2.3",
			"1.2.4",
			"1.2.6",
			fmt.Errorf("rkt: API version is too old(%v), requires at least %v", fr.info.ApiVersion, "1.2.6"),
		},
		// Requires greater API version.
		{
			"1.2.3",
			"1.2.3",
			"1.2.4",
			"1.2.7",
			fmt.Errorf("rkt: API version is too old(%v), requires at least %v", fr.info.ApiVersion, "1.2.7"),
		},
	}

	for i, tt := range tests {
		testCaseHint := fmt.Sprintf("test case #%d", i)
		err := r.checkVersion(tt.minimumRktBinVersion, tt.recommendRktBinVersion, tt.minimumAppcVersion, tt.minimumRktApiVersion)
		assert.Equal(t, err, tt.err, testCaseHint)

		if err == nil {
			assert.Equal(t, r.binVersion.String(), fr.info.RktVersion, testCaseHint)
			assert.Equal(t, r.appcVersion.String(), fr.info.AppcVersion, testCaseHint)
			assert.Equal(t, r.apiVersion.String(), fr.info.ApiVersion, testCaseHint)

		}
	}
}
