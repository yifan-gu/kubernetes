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

// Package generators has the generators for the client-gen utility.
package generators

import (
	"os"
	"path/filepath"
	"strings"

	"k8s.io/kubernetes/cmd/libs/go2idl/args"
	"k8s.io/kubernetes/cmd/libs/go2idl/client-gen/generators/fake"
	"k8s.io/kubernetes/cmd/libs/go2idl/generator"
	"k8s.io/kubernetes/cmd/libs/go2idl/namer"
	"k8s.io/kubernetes/cmd/libs/go2idl/types"
	"k8s.io/kubernetes/pkg/api/unversioned"

	"github.com/golang/glog"
)

// ClientGenArgs is a wrapper for arguments to client-gen.
type ClientGenArgs struct {
	// TODO: we should make another type declaration of GroupVersion out of the
	// unversioned package, which is part of our API. Tools like client-gen
	// shouldn't depend on an API.
	GroupVersions []unversioned.GroupVersion
	// ClientsetName is the name of the clientset to be generated. It's
	// populated from command-line arguments.
	ClientsetName string
	// ClientsetOutputPath is the path the clientset will be generated at. It's
	// populated from command-line arguments.
	ClientsetOutputPath string
	// ClientsetOnly determines if we should generate the clients for groups and
	// types along with the clientset. It's populated from command-line
	// arguments.
	ClientsetOnly bool
	// FakeClient determines if client-gen generates the fake clients.
	FakeClient bool
}

// NameSystems returns the name system used by the generators in this package.
func NameSystems() namer.NameSystems {
	pluralExceptions := map[string]string{
		"Endpoints": "Endpoints",
	}
	return namer.NameSystems{
		"public":             namer.NewPublicNamer(0),
		"private":            namer.NewPrivateNamer(0),
		"raw":                namer.NewRawNamer("", nil),
		"publicPlural":       namer.NewPublicPluralNamer(pluralExceptions),
		"privatePlural":      namer.NewPrivatePluralNamer(pluralExceptions),
		"allLowercasePlural": namer.NewAllLowercasePluralNamer(pluralExceptions),
	}
}

// DefaultNameSystem returns the default name system for ordering the types to be
// processed by the generators in this package.
func DefaultNameSystem() string {
	return "public"
}

func packageForGroup(group string, version string, typeList []*types.Type, packageBasePath string, srcTreePath string, boilerplate []byte) generator.Package {
	outputPackagePath := filepath.Join(packageBasePath, group, version)
	return &generator.DefaultPackage{
		PackageName: version,
		PackagePath: outputPackagePath,
		HeaderText:  boilerplate,
		PackageDocumentation: []byte(
			`// Package unversioned has the automatically generated clients for unversioned resources.
`),
		// GeneratorFunc returns a list of generators. Each generator makes a
		// single file.
		GeneratorFunc: func(c *generator.Context) (generators []generator.Generator) {
			generators = []generator.Generator{
				// Always generate a "doc.go" file.
				generator.DefaultGen{OptionalName: "doc"},
			}
			// Since we want a file per type that we generate a client for, we
			// have to provide a function for this.
			for _, t := range typeList {
				generators = append(generators, &genClientForType{
					DefaultGen: generator.DefaultGen{
						OptionalName: strings.ToLower(c.Namers["private"].Name(t)),
					},
					outputPackage: outputPackagePath,
					group:         group,
					typeToMatch:   t,
					imports:       generator.NewImportTracker(),
				})
			}

			generators = append(generators, &genGroup{
				DefaultGen: generator.DefaultGen{
					OptionalName: group + "_client",
				},
				outputPackage: outputPackagePath,
				group:         group,
				types:         typeList,
				imports:       generator.NewImportTracker(),
			})

			expansionFileName := "generated_expansion"
			// To avoid overriding user's manual modification, only generate the expansion file if it doesn't exist.
			if _, err := os.Stat(filepath.Join(srcTreePath, outputPackagePath, expansionFileName+".go")); os.IsNotExist(err) {
				generators = append(generators, &genExpansion{
					DefaultGen: generator.DefaultGen{
						OptionalName: expansionFileName,
					},
					types: typeList,
				})
			}

			return generators
		},
		FilterFunc: func(c *generator.Context, t *types.Type) bool {
			return types.ExtractCommentTags("+", t.SecondClosestCommentLines)["genclient"] == "true"
		},
	}
}

func packageForClientset(customArgs ClientGenArgs, typedClientBasePath string, boilerplate []byte) generator.Package {
	return &generator.DefaultPackage{
		PackageName: customArgs.ClientsetName,
		PackagePath: filepath.Join(customArgs.ClientsetOutputPath, customArgs.ClientsetName),
		HeaderText:  boilerplate,
		PackageDocumentation: []byte(
			`// This package has the automatically generated clientset.
`),
		// GeneratorFunc returns a list of generators. Each generator generates a
		// single file.
		GeneratorFunc: func(c *generator.Context) (generators []generator.Generator) {
			generators = []generator.Generator{
				&genClientset{
					DefaultGen: generator.DefaultGen{
						OptionalName: "clientset",
					},
					groupVersions:   customArgs.GroupVersions,
					typedClientPath: typedClientBasePath,
					outputPackage:   customArgs.ClientsetName,
					imports:         generator.NewImportTracker(),
				},
			}
			return generators
		},
	}
}

// Packages makes the client package definition.
func Packages(context *generator.Context, arguments *args.GeneratorArgs) generator.Packages {
	boilerplate, err := arguments.LoadGoBoilerplate()
	if err != nil {
		glog.Fatalf("Failed loading boilerplate: %v", err)
	}

	groupToTypes := map[string][]*types.Type{}
	for _, inputDir := range arguments.InputDirs {
		p := context.Universe.Package(inputDir)
		for _, t := range p.Types {
			if types.ExtractCommentTags("+", t.SecondClosestCommentLines)["genclient"] != "true" {
				continue
			}
			group := filepath.Base(t.Name.Package)
			// Special case for the core API.
			if group == "api" {
				group = "core"
			}
			if _, found := groupToTypes[group]; !found {
				groupToTypes[group] = []*types.Type{}
			}
			groupToTypes[group] = append(groupToTypes[group], t)
		}
	}

	customArgs, ok := arguments.CustomArgs.(ClientGenArgs)
	if !ok {
		glog.Fatalf("cannot convert arguments.CustomArgs to ClientGenArgs")
	}

	var packageList []generator.Package

	packageList = append(packageList, packageForClientset(customArgs, arguments.OutputPackagePath, boilerplate))
	if customArgs.FakeClient {
		packageList = append(packageList, fake.PackageForClientset(arguments.OutputPackagePath, customArgs.GroupVersions, boilerplate))
	}

	// If --clientset-only=true, we don't regenerate the individual typed clients.
	if customArgs.ClientsetOnly {
		return generator.Packages(packageList)
	}

	orderer := namer.Orderer{namer.NewPrivateNamer(0)}
	for group, types := range groupToTypes {
		packageList = append(packageList, packageForGroup(group, "unversioned", orderer.OrderTypes(types), arguments.OutputPackagePath, arguments.OutputBase, boilerplate))
		if customArgs.FakeClient {
			packageList = append(packageList, fake.PackageForGroup(group, "unversioned", orderer.OrderTypes(types), arguments.OutputPackagePath, arguments.OutputBase, boilerplate))
		}
	}

	return generator.Packages(packageList)
}
