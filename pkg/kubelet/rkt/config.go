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
	"path/filepath"

	rktapi "github.com/coreos/rkt/api/v1alpha"
	"golang.org/x/net/context"
)

const (
	// Default name of the stage1-fly image.
	// It is expected to be located under the same directory as the rkt binary.
	defaultStage1FlyName = "stage1-fly.aci"
)

// Config stores the global configuration for the rkt runtime.
// Detailed documents can be found at:
// https://github.com/coreos/rkt/blob/master/Documentation/commands.md#global-options
type Config struct {
	// The absolute path to the binary, or leave empty to find it in $PATH.
	Path string
	// The rkt data directory.
	Dir string
	// The image to use as stage1.
	Stage1Image string
	// The debug flag for rkt.
	Debug bool
	// Comma-separated list of security features to disable.
	// Allowed values: "none", "image", "tls", "ondisk", "http", "all".
	InsecureOptions string
	// The local config directory.
	LocalConfigDir string
	// The user config directory.
	UserConfigDir string
	// The system config directory.
	SystemConfigDir string
}

// buildGlobalOptions returns an array of global command line options.
func (c *Config) buildGlobalOptions() []string {
	var result []string
	if c == nil {
		return result
	}

	if c.Debug {
		result = append(result, "--debug=true")
	}
	if c.InsecureOptions != "" {
		result = append(result, fmt.Sprintf("--insecure-options=%s", c.InsecureOptions))
	}
	if c.LocalConfigDir != "" {
		result = append(result, fmt.Sprintf("--local-config=%s", c.LocalConfigDir))
	}
	if c.UserConfigDir != "" {
		result = append(result, fmt.Sprintf("--user-config=%s", c.UserConfigDir))
	}
	if c.SystemConfigDir != "" {
		result = append(result, fmt.Sprintf("--system-config=%s", c.SystemConfigDir))
	}
	if c.Dir != "" {
		result = append(result, fmt.Sprintf("--dir=%s", c.Dir))
	}
	return result
}

// getConfig gets configurations from the rkt API service
// and merge it with the existing config. The merge rule is
// that the fields in the provided config will override the
// result that get from the rkt api service.
func (r *Runtime) getConfig(cfg *Config) (*Config, error) {
	resp, err := r.apisvc.GetInfo(context.Background(), &rktapi.GetInfoRequest{})
	if err != nil {
		return nil, err
	}

	flags := resp.Info.GlobalFlags

	if cfg.Dir == "" {
		cfg.Dir = flags.Dir
	}
	if cfg.InsecureOptions == "" {
		cfg.InsecureOptions = flags.InsecureFlags
	}
	if cfg.LocalConfigDir == "" {
		cfg.LocalConfigDir = flags.LocalConfigDir
	}
	if cfg.UserConfigDir == "" {
		cfg.UserConfigDir = flags.UserConfigDir
	}
	if cfg.SystemConfigDir == "" {
		cfg.SystemConfigDir = flags.SystemConfigDir
	}

	return cfg, nil
}

// getDefaultStage1FlyPath returns the stage1-fly image's absolute path.
func (r *Runtime) getDefaultStage1FlyPath() string {
	return filepath.Join(filepath.Dir(r.config.Path), defaultStage1FlyName)
}
