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

package controlplane_test

import (
	"errors"

	"k8s.io/client-go/rest"

	"github.com/kcp-dev/multicluster-provider/envtest/internal/process"

	. "github.com/kcp-dev/multicluster-provider/envtest/internal/controlplane"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Shard", func() {
	var server *Shard
	BeforeEach(func() {
		server = &Shard{}
	})
	JustBeforeEach(func() {
		Expect(PrepareShard(server)).To(Succeed())
	})
	Describe("setting up serving hosts & ports", func() {
		Context("when SecureServing host & port are set", func() {
			BeforeEach(func() {
				server.Address = "localhost"
				server.Port = "8675"
			})

			It("should leave SecureServing as-is", func() {
				Expect(server.SecureServing.Address).To(Equal("localhost"))
				Expect(server.SecureServing.Port).To(Equal("8675"))
			})
		})

		Context("when SecureServing is not set", func() {
			It("should be defaulted with a random port", func() {
				Expect(server.Port).NotTo(BeEquivalentTo(0))
			})
		})
	})

	It("should default authn if not set", func() {
		Expect(server.Authn).NotTo(BeNil())
	})

	Describe("setting up auth", func() {
		var auth *fakeAuthn
		BeforeEach(func() {
			auth = &fakeAuthn{
				setFlag: true,
			}
			server.Authn = auth
		})
		It("should configure with the root dir", func() {
			Expect(auth.workDir).To(Equal(server.RootDir))
		})
		It("should pass its args to be configured", func() {
			Expect(server.Configure().Get("configure-called").Get(nil)).To(ConsistOf("true"))
		})

		Context("when configuring auth errors out", func() {
			It("should fail to configure", func() {
				server := &Shard{
					SecureServing: SecureServing{
						Authn: auth,
					},
				}
				auth.configureErr = errors.New("Oh no")
				Expect(PrepareShard(server)).NotTo(Succeed())
			})
		})
	})

	Describe("managing", func() {
		// some of these tests are combined for speed reasons -- starting the apiserver
		// takes a while, relatively speaking

		var (
			auth *fakeAuthn
		)
		BeforeEach(func() {
			auth = &fakeAuthn{}
			server.Authn = auth
		})

		Context("after starting", func() {
			BeforeEach(func() {
				Expect(server.Start()).To(Succeed())
			})

			It("should stop successfully, and stop auth", func() {
				Expect(server.Stop()).To(Succeed())
				Expect(auth.stopCalled).To(BeTrue())
			})
		})

		It("should fail to start when auth fails to start", func() {
			auth.startErr = errors.New("Oh no")
			Expect(server.Start()).NotTo(Succeed())
		})

		It("should start successfully & start auth", func() {
			Expect(server.Start()).To(Succeed())
			defer func() { Expect(server.Stop()).To(Succeed()) }()
			Expect(auth.startCalled).To(BeTrue())
		})
	})
})

type fakeAuthn struct {
	workDir string

	startCalled bool
	stopCalled  bool
	setFlag     bool

	configureErr error
	startErr     error
}

func (f *fakeAuthn) Configure(workDir string, args *process.Arguments) error {
	f.workDir = workDir
	if f.setFlag {
		args.Set("configure-called", "true")
	}
	return f.configureErr
}
func (f *fakeAuthn) Start() error {
	f.startCalled = true
	return f.startErr
}
func (f *fakeAuthn) AddUser(user User, baseCfg *rest.Config) (*rest.Config, error) {
	return nil, nil
}
func (f *fakeAuthn) Stop() error {
	f.stopCalled = true
	return nil
}
