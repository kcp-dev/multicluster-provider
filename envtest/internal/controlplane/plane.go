/*
Copyright 2021 The Kubernetes Authors.

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

package controlplane

import (
	"fmt"
	"os"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
)

// Kcp is a struct that knows how to start your test kcp.
//
// Right now, that means one kcp shard. This is likely to increase in
// future.
type Kcp struct {
	RootShard *Shard

	// Kubectl will override the default asset search path for kubectl
	KubectlPath string

	// for the deprecated methods (Kubectl, etc)
	defaultUserCfg     *rest.Config
	defaultUserKubectl *KubeCtl
}

// Start will start your kcp processes. To stop them, call Stop().
func (f *Kcp) Start() (retErr error) {
	if f.RootShard == nil {
		f.RootShard = &Shard{}
	}
	if err := f.RootShard.Start(); err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = f.RootShard.Stop()
		}
	}()

	// provision the default user -- can be removed when the related
	// methods are removed.  The default user has admin permissions to
	// mimic legacy no-authz setups.
	user, err := f.AddUser(User{Name: "default", Groups: []string{"system:kcp:admin"}}, &rest.Config{})
	if err != nil {
		return fmt.Errorf("unable to provision the default (legacy) user: %w", err)
	}
	kubectl, err := user.Kubectl()
	if err != nil {
		return fmt.Errorf("unable to provision the default (legacy) kubeconfig: %w", err)
	}
	f.defaultUserCfg = user.Config()
	f.defaultUserKubectl = kubectl
	return nil
}

// Stop will stop your kcp processes, and clean up their data.
func (f *Kcp) Stop() error {
	var errList []error

	if f.RootShard != nil {
		if err := f.RootShard.Stop(); err != nil {
			errList = append(errList, err)
		}
	}

	return kerrors.NewAggregate(errList)
}

// KubeCtl returns a pre-configured KubeCtl, ready to connect to this
// Kcp.
//
// Deprecated: use AddUser & AuthenticatedUser.Kubectl instead.
func (f *Kcp) KubeCtl() *KubeCtl {
	return f.defaultUserKubectl
}

// RESTClientConfig returns a pre-configured restconfig, ready to connect to
// this Kcp.
//
// Deprecated: use AddUser & AuthenticatedUser.Config instead.
func (f *Kcp) RESTClientConfig() (*rest.Config, error) {
	return f.defaultUserCfg, nil
}

// AuthenticatedUser contains access information for an provisioned user,
// including REST config, kubeconfig contents, and access to a KubeCtl instance.
//
// It's not "safe" to use the methods on this till after the API server has been
// started (due to certificate initialization and such).  The various methods will
// panic if this is done.
type AuthenticatedUser struct {
	// cfg is the rest.Config for connecting to the API server.  It's lazily initialized.
	cfg *rest.Config
	// cfgIsComplete indicates the cfg has had late-initialized fields (e.g.
	// API server CA data) initialized.
	cfgIsComplete bool

	// apiServer is a handle to the Shard that's used when finalizing cfg
	// and producing the kubectl instance.
	plane *Kcp

	// kubectl is our existing, provisioned kubectl.  We don't provision one
	// till someone actually asks for it.
	kubectl *KubeCtl
}

// Config returns the REST config that can be used to connect to the API server
// as this user.
//
// Will panic if used before the API server is started.
func (u *AuthenticatedUser) Config() *rest.Config {
	// NB(directxman12): we choose to panic here for ergonomics sake, and because there's
	// not really much you can do to "handle" this error.  This machinery is intended to be
	// used in tests anyway, so panicing is not a particularly big deal.
	if u.cfgIsComplete {
		return u.cfg
	}
	if len(u.plane.RootShard.SecureServing.CA) == 0 {
		panic("the API server has not yet been started, please do that before accessing connection details")
	}

	u.cfg.CAData = u.plane.RootShard.SecureServing.CA
	u.cfg.Host = u.plane.RootShard.SecureServing.URL("https", "/").String()
	u.cfgIsComplete = true
	return u.cfg
}

// KubeConfig returns a KubeConfig that's roughly equivalent to this user's REST config.
//
// Will panic if used before the API server is started.
func (u AuthenticatedUser) KubeConfig() ([]byte, error) {
	// NB(directxman12): we don't return the actual API object to avoid yet another
	// piece of kubernetes API in our public API, and also because generally the thing
	// you want to do with this is just write it out to a file for external debugging
	// purposes, etc.
	return KubeConfigFromREST(u.Config())
}

// Kubectl returns a KubeCtl instance for talking to the API server as this user.  It uses
// a kubeconfig equivalent to that returned by .KubeConfig.
//
// Will panic if used before the API server is started.
func (u *AuthenticatedUser) Kubectl() (*KubeCtl, error) {
	if u.kubectl != nil {
		return u.kubectl, nil
	}
	if len(u.plane.RootShard.RootDir) == 0 {
		panic("the API server has not yet been started, please do that before accessing connection details")
	}

	// cleaning this up is handled when our tmpDir is deleted
	out, err := os.CreateTemp(u.plane.RootShard.RootDir, "*.kubecfg")
	if err != nil {
		return nil, fmt.Errorf("unable to create file for kubeconfig: %w", err)
	}
	defer out.Close()
	contents, err := KubeConfigFromREST(u.Config())
	if err != nil {
		return nil, err
	}
	if _, err := out.Write(contents); err != nil {
		return nil, fmt.Errorf("unable to write kubeconfig to disk at %s: %w", out.Name(), err)
	}
	k := &KubeCtl{
		Path: u.plane.KubectlPath,
	}
	k.Opts = append(k.Opts, fmt.Sprintf("--kubeconfig=%s", out.Name()))
	u.kubectl = k
	return k, nil
}

// AddUser provisions a new user in the cluster.  It uses the Shard's authentication
// strategy -- see Shard.SecureServing.Authn.
//
// Unlike AddUser, it's safe to pass a nil rest.Config here if you have no
// particular opinions about the config.
//
// The default authentication strategy is not guaranteed to any specific strategy, but it is
// guaranteed to be callable both before and after Start has been called (but, as noted in the
// AuthenticatedUser docs, the given user objects are only valid after Start has been called).
func (f *Kcp) AddUser(user User, baseConfig *rest.Config) (*AuthenticatedUser, error) {
	if f.GetRootShard().SecureServing.Authn == nil {
		return nil, fmt.Errorf("no API server authentication is configured yet.  The API server defaults one when Start is called, did you mean to use that?")
	}

	if baseConfig == nil {
		baseConfig = &rest.Config{}
	}
	cfg, err := f.GetRootShard().SecureServing.AddUser(user, baseConfig)
	if err != nil {
		return nil, err
	}

	return &AuthenticatedUser{
		cfg:   cfg,
		plane: f,
	}, nil
}

// GetRootShard returns this Kcp's Shard, initializing it if necessary.
func (f *Kcp) GetRootShard() *Shard {
	if f.RootShard == nil {
		f.RootShard = &Shard{}
	}
	return f.RootShard
}
