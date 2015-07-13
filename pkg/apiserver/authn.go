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

package apiserver

import (
	"crypto/rsa"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/auth/authenticator"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/auth/authenticator/bearertoken"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/serviceaccount"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/auth/authenticator/password/passwordfile"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/auth/authenticator/request/basicauth"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/auth/authenticator/request/union"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/auth/authenticator/request/x509"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/auth/authenticator/token/openid"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/auth/authenticator/token/tokenfile"
)

type AuthenticatorConfig struct {
	BasicAuthFile         string
	ClientCAFile          string
	TokenAuthFile         string
	OpenIDClientID        string
	OpenIDEndpoint        string
	ServiceAccountKeyFile string
	ServiceAccountLookup  bool
}

// NewAuthenticator returns an authenticator.Request or an error
func NewAuthenticator(config AuthenticatorConfig, helper tools.EtcdHelper) (authenticator.Request, error) {
	var authenticators []authenticator.Request

	if len(config.BasicAuthFile) > 0 {
		basicAuth, err := newAuthenticatorFromBasicAuthFile(config.BasicAuthFile)
		if err != nil {
			return nil, err
		}
		authenticators = append(authenticators, basicAuth)
	}

	if len(config.ClientCAFile) > 0 {
		certAuth, err := newAuthenticatorFromClientCAFile(config.ClientCAFile)
		if err != nil {
			return nil, err
		}
		authenticators = append(authenticators, certAuth)
	}

	if len(config.TokenAuthFile) > 0 {
		tokenAuth, err := newAuthenticatorFromTokenFile(config.TokenAuthFile)
		if err != nil {
			return nil, err
		}
		authenticators = append(authenticators, tokenAuth)
	}

	if len(config.OpenIDEndpoint) > 0 && len(config.OpenIDClientID) > 0 {
		openIDAuth, err := newAuthenticatorFromOpenIDEndpoint(config.OpenIDClientID, config.OpenIDEndpoint)
		if err != nil {
			return nil, err
		}
		authenticators = append(authenticators, openIDAuth)
	}

	if len(config.ServiceAccountKeyFile) > 0 {
		serviceAccountAuth, err := newServiceAccountAuthenticator(config.ServiceAccountKeyFile, config.ServiceAccountLookup, helper)
		if err != nil {
			return nil, err
		}
		authenticators = append(authenticators, serviceAccountAuth)
	}

	switch len(authenticators) {
	case 0:
		return nil, nil
	case 1:
		return authenticators[0], nil
	default:
		return union.New(authenticators...), nil
	}
}

// IsValidServiceAccountKeyFile returns true if a valid public RSA key can be read from the given file
func IsValidServiceAccountKeyFile(file string) bool {
	_, err := serviceaccount.ReadPublicKey(file)
	return err == nil
}

// newAuthenticatorFromBasicAuthFile returns an authenticator.Request or an error
func newAuthenticatorFromBasicAuthFile(basicAuthFile string) (authenticator.Request, error) {
	basicAuthenticator, err := passwordfile.NewCSV(basicAuthFile)
	if err != nil {
		return nil, err
	}

	return basicauth.New(basicAuthenticator), nil
}

// newAuthenticatorFromTokenFile returns an authenticator.Request or an error
func newAuthenticatorFromTokenFile(tokenAuthFile string) (authenticator.Request, error) {
	tokenAuthenticator, err := tokenfile.NewCSV(tokenAuthFile)
	if err != nil {
		return nil, err
	}

	return bearertoken.New(tokenAuthenticator), nil
}

// newAuthenticatorFromOpenIDEndpoint returns an authenticator.Request or an error.
func newAuthenticatorFromOpenIDEndpoint(clientID, endpoint string) (authenticator.Request, error) {
	tokenAuthenticator, err := openid.NewOpenID(clientID, endpoint)
	if err != nil {
		return nil, err
	}

	return bearertoken.New(tokenAuthenticator), nil
}

// newServiceAccountAuthenticator returns an authenticator.Request or an error
func newServiceAccountAuthenticator(keyfile string, lookup bool, helper tools.EtcdHelper) (authenticator.Request, error) {
	publicKey, err := serviceaccount.ReadPublicKey(keyfile)
	if err != nil {
		return nil, err
	}

	var serviceAccountGetter serviceaccount.ServiceAccountTokenGetter
	if lookup {
		// If we need to look up service accounts and tokens,
		// go directly to etcd to avoid recursive auth insanity
		serviceAccountGetter = serviceaccount.NewGetterFromEtcdHelper(helper)
	}

	tokenAuthenticator := serviceaccount.JWTTokenAuthenticator([]*rsa.PublicKey{publicKey}, lookup, serviceAccountGetter)
	return bearertoken.New(tokenAuthenticator), nil
}

// newAuthenticatorFromClientCAFile returns an authenticator.Request or an error
func newAuthenticatorFromClientCAFile(clientCAFile string) (authenticator.Request, error) {
	roots, err := util.CertPoolFromFile(clientCAFile)
	if err != nil {
		return nil, err
	}

	opts := x509.DefaultVerifyOptions()
	opts.Roots = roots

	return x509.New(opts, x509.CommonNameUserConversion), nil
}
