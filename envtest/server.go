/*
Copyright 2016 The Kubernetes Authors.

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

package envtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kcp-dev/multicluster-provider/envtest/internal/controlplane"
	"github.com/kcp-dev/multicluster-provider/envtest/internal/process"
)

var log = ctrllog.Log.WithName("envtest")

/*
It's possible to override some defaults, by setting the following environment variables:
* USE_EXISTING_KCP (boolean): if set to true, envtest will use an existing kcp.
* TEST_ASSET_KCP (string): path to the kcp binary to use
* TEST_KCP_ASSETS (string): directory containing the binaries to use (kcp). Defaults to /usr/local/kcp/bin.
* TEST_KCP_START_TIMEOUT (string supported by time.ParseDuration): timeout for test kcp to start. Defaults to 1m.
* TEST_KCP_STOP_TIMEOUT (string supported by time.ParseDuration): timeout for test kcp to start. Defaults to 20s.
* TEST_ATTACH_KCP_OUTPUT (boolean): if set to true, the kcp's stdout and stderr are attached to os.Stdout and os.Stderr
*/
const (
	envUseExistingCluster = "USE_EXISTING_KCP"
	envAttachOutput       = "TEST_ATTACH_KCP_OUTPUT"
	envStartTimeout       = "TEST_KCP_START_TIMEOUT"
	envStopTimeout        = "TEST_KCP_STOP_TIMEOUT"

	defaultKcpPlaneStartTimeout = time.Minute
	defaultKcpStopTimeout       = 20 * time.Second
)

// internal types we expose as part of our public API.
type (
	// Kcp is the re-exported Kcp type from the internal testing package.
	Kcp = controlplane.Kcp

	// Shard is the re-exported Shard from the internal testing package.
	Shard = controlplane.Shard

	// User represents a Kubernetes user to provision for auth purposes.
	User = controlplane.User

	// AuthenticatedUser represets a Kubernetes user that's been provisioned.
	AuthenticatedUser = controlplane.AuthenticatedUser

	// ListenAddr indicates the address and port that the API server should listen on.
	ListenAddr = process.ListenAddr

	// SecureServing contains details describing how the API server should serve
	// its secure endpoint.
	SecureServing = controlplane.SecureServing

	// Authn is an authentication method that can be used with the control plane to
	// provision users.
	Authn = controlplane.Authn

	// Arguments allows configuring a process's flags.
	Arguments = process.Arguments

	// Arg is a single flag with one or more values.
	Arg = process.Arg
)

var (
	// EmptyArguments constructs a new set of flags with nothing set.
	//
	// This is mostly useful for testing helper methods -- you'll want to call
	// Configure on the APIServer (or etcd) to configure their arguments.
	EmptyArguments = process.EmptyArguments
)

// Environment creates a Kubernetes test environment that will start / stop the Kubernetes control plane and
// install extension APIs.
type Environment struct {
	// Kcp is the Kcp instance.
	Kcp controlplane.Kcp

	// Scheme is used to determine if conversion webhooks should be enabled
	// for a particular CRD / object.
	//
	// Conversion webhooks are going to be enabled if an object in the scheme
	// implements Hub and Spoke conversions.
	//
	// If nil, scheme.Scheme is used.
	Scheme *runtime.Scheme

	// Config can be used to talk to the apiserver.  It's automatically
	// populated if not set using the standard controller-runtime config
	// loading.
	Config *rest.Config

	// BinaryAssetsDirectory is the path where the binaries required for the envtest are
	// located in the local environment. This field can be overridden by setting TEST_KCP_ASSETS.
	BinaryAssetsDirectory string

	// UseExistingCluster indicates that this environments should use an
	// existing kubeconfig, instead of trying to stand up a new kcp.
	// It defaults to the USE_EXISTING_KCP environment variable if unspecified.
	UseExistingKcp *bool

	// KcpStartTimeout is the maximum duration each kcp component
	// may take to start. It defaults to the TEST_KCP_START_TIMEOUT
	// environment variable or 20 seconds if unspecified.
	KcpStartTimeout time.Duration

	// KcpStopTimeout is the maximum duration each kcp component
	// may take to stop. It defaults to the TEST_KCP_STOP_TIMEOUT
	// environment variable or 20 seconds if unspecified.
	KcpStopTimeout time.Duration

	// AttachKcpOutput indicates if kcp output will be attached to os.Stdout and os.Stderr.
	// Enable this to get more visibility of the testing kcp.
	// It respects the the TEST_ATTACH_KCP_OUTPUT environment variable.
	AttachKcpOutput bool
}

// Stop stops a running server.
// Previously installed CRDs, as listed in CRDInstallOptions.CRDs, will be uninstalled
// if CRDInstallOptions.CleanUpAfterUse are set to true.
func (te *Environment) Stop() error {
	if te.useExistingKcp() {
		return nil
	}

	return te.Kcp.Stop()
}

// Start starts a local Kubernetes server and updates te.ApiserverPort with the port it is listening on.
func (te *Environment) Start() (*rest.Config, error) {
	if te.useExistingKcp() {
		log.V(1).Info("using existing cluster")
		if te.Config == nil {
			// we want to allow people to pass in their own config, so
			// only load a config if it hasn't already been set.
			log.V(1).Info("automatically acquiring client configuration")

			var err error
			te.Config, err = config.GetConfig()
			if err != nil {
				return nil, fmt.Errorf("unable to get configuration for existing cluster: %w", err)
			}

			if strings.Contains(te.Config.Host, "/clusters/") {
				return nil, fmt.Errorf("'%s' contains /clusters/ but should point to base context", te.Config.Host)
			}
		}
	} else {
		shard := te.Kcp.GetRootShard()

		if os.Getenv(envAttachOutput) == "true" {
			te.AttachKcpOutput = true
		}
		if te.AttachKcpOutput {
			if shard.Out == nil {
				shard.Out = os.Stdout
			}
			if shard.Err == nil {
				shard.Err = os.Stderr
			}
		}

		shard.Path = process.BinPathFinder("kcp", te.BinaryAssetsDirectory)
		te.Kcp.KubectlPath = process.BinPathFinder("kubectl", te.BinaryAssetsDirectory)

		if err := te.defaultTimeouts(); err != nil {
			return nil, fmt.Errorf("failed to default controlplane timeouts: %w", err)
		}
		shard.StartTimeout = te.KcpStartTimeout
		shard.StopTimeout = te.KcpStopTimeout

		log.V(1).Info("starting control plane")
		if err := te.startControlPlane(); err != nil {
			return nil, fmt.Errorf("unable to start control plane itself: %w", err)
		}

		// Create the *rest.Config for creating new clients
		baseConfig := &rest.Config{
			// gotta go fast during tests -- we don't really care about overwhelming our test API server
			QPS:   1000.0,
			Burst: 2000.0,
		}

		adminInfo := User{Name: "admin", Groups: []string{"system:kcp:admin"}}
		adminUser, err := te.Kcp.AddUser(adminInfo, baseConfig)
		if err != nil {
			return te.Config, fmt.Errorf("unable to provision admin user: %w", err)
		}
		te.Config = adminUser.Config()
	}

	// Set the default scheme if nil.
	if te.Scheme == nil {
		te.Scheme = scheme.Scheme
	}

	// If we are bringing etcd up for the first time, it can take some time for the
	// default namespace to actually be created and seen as available to the apiserver
	if err := te.waitForRootWorkspaceDefaultNamespace(te.Config); err != nil {
		return nil, fmt.Errorf("default namespace didn't register within deadline: %w", err)
	}

	return te.Config, nil
}

// AddUser provisions a new user for connecting to this Environment.  The user will
// have the specified name & belong to the specified groups.
//
// If you specify a "base" config, the returned REST Config will contain those
// settings as well as any required by the authentication method.  You can use
// this to easily specify options like QPS.
//
// This is effectively a convinience alias for Kcp.AddUser -- see that
// for more low-level details.
func (te *Environment) AddUser(user User, baseConfig *rest.Config) (*AuthenticatedUser, error) {
	return te.Kcp.AddUser(user, baseConfig)
}

func (te *Environment) startControlPlane() error {
	numTries, maxRetries := 0, 5
	var err error
	for ; numTries < maxRetries; numTries++ {
		// Start the control plane - retry if it fails
		err = te.Kcp.Start()
		if err == nil {
			break
		}
		log.Error(err, "unable to start the controlplane", "tries", numTries)
	}
	if numTries == maxRetries {
		return fmt.Errorf("failed to start the controlplane. retried %d times: %w", numTries, err)
	}
	return nil
}

func (te *Environment) waitForRootWorkspaceDefaultNamespace(config *rest.Config) error {
	cfg := rest.CopyConfig(config)
	cfg.Host += "/clusters/root"
	cs, err := client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("unable to create client: %w", err)
	}
	// It shouldn't take longer than 5s for the default namespace to be brought up in etcd
	return wait.PollUntilContextTimeout(context.TODO(), time.Millisecond*50, time.Second*5, true, func(ctx context.Context) (bool, error) {
		if err = cs.Get(ctx, types.NamespacedName{Name: "default"}, &corev1.Namespace{}); err != nil {
			log.V(1).Info("Waiting for default namespace to be available", "error", err)
			return false, nil //nolint:nilerr
		}
		return true, nil
	})
}

func (te *Environment) defaultTimeouts() error {
	var err error
	if te.KcpStartTimeout == 0 {
		if envVal := os.Getenv(envStartTimeout); envVal != "" {
			te.KcpStartTimeout, err = time.ParseDuration(envVal)
			if err != nil {
				return err
			}
		} else {
			te.KcpStartTimeout = defaultKcpPlaneStartTimeout
		}
	}

	if te.KcpStopTimeout == 0 {
		if envVal := os.Getenv(envStopTimeout); envVal != "" {
			te.KcpStopTimeout, err = time.ParseDuration(envVal)
			if err != nil {
				return err
			}
		} else {
			te.KcpStopTimeout = defaultKcpStopTimeout
		}
	}
	return nil
}

func (te *Environment) useExistingKcp() bool {
	if te.UseExistingKcp == nil {
		return strings.ToLower(os.Getenv(envUseExistingCluster)) == "true"
	}
	return *te.UseExistingKcp
}
