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

package process_test

import (
	"strings"

	. "github.com/kcp-dev/multicluster-provider/envtest/internal/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Arguments", func() {
	Context("when appending", func() {
		It("should copy from defaults when appending for the first time", func() {
			args := EmptyArguments().
				Append("some-key", "val3")
			Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"val1", "val2", "val3"}))
		})

		It("should not copy from defaults if the flag has been disabled previously", func() {
			args := EmptyArguments().
				Disable("some-key").
				Append("some-key", "val3")
			Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"val3"}))
		})

		It("should only copy defaults the first time", func() {
			args := EmptyArguments().
				Append("some-key", "val3", "val4").
				Append("some-key", "val5")
			Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"val1", "val2", "val3", "val4", "val5"}))
		})

		It("should not copy from defaults if the flag has been previously overridden", func() {
			args := EmptyArguments().
				Set("some-key", "vala").
				Append("some-key", "valb", "valc")
			Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"vala", "valb", "valc"}))
		})

		Context("when explicitly overriding defaults", func() {
			It("should not copy from defaults, but should append to previous calls", func() {
				args := EmptyArguments().
					AppendNoDefaults("some-key", "vala").
					AppendNoDefaults("some-key", "valb", "valc")
				Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"vala", "valb", "valc"}))
			})

			It("should not copy from defaults, but should respect previous appends' copies", func() {
				args := EmptyArguments().
					Append("some-key", "vala").
					AppendNoDefaults("some-key", "valb", "valc")
				Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"val1", "val2", "vala", "valb", "valc"}))
			})

			It("should not copy from defaults if the flag has been previously appended to ignoring defaults", func() {
				args := EmptyArguments().
					AppendNoDefaults("some-key", "vala").
					Append("some-key", "valb", "valc")
				Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"vala", "valb", "valc"}))
			})
		})
	})

	It("should ignore defaults when overriding", func() {
		args := EmptyArguments().
			Set("some-key", "vala")
		Expect(args.Get("some-key").Get([]string{"val1", "val2"})).To(Equal([]string{"vala"}))
	})

	It("should allow directly setting the argument value for custom argument types", func() {
		args := EmptyArguments().
			SetRaw("custom-key", commaArg{"val3"}).
			Append("custom-key", "val4")
		Expect(args.Get("custom-key").Get([]string{"val1", "val2"})).To(Equal([]string{"val1,val2,val3,val4"}))
	})

	Context("when rendering flags", func() {
		It("should not render defaults for disabled flags", func() {
			defs := map[string][]string{
				"some-key":  {"val1", "val2"},
				"other-key": {"val"},
			}
			args := EmptyArguments().
				Disable("some-key")
			Expect(args.AsStrings(defs)).To(ConsistOf("--other-key=val"))
		})

		It("should render name-only flags as --key", func() {
			args := EmptyArguments().
				Enable("some-key")
			Expect(args.AsStrings(nil)).To(ConsistOf("--some-key"))
		})

		It("should render multiple values as --key=val1, --key=val2", func() {
			args := EmptyArguments().
				Append("some-key", "val1", "val2").
				Append("other-key", "vala", "valb")
			Expect(args.AsStrings(nil)).To(ConsistOf("--other-key=valb", "--other-key=vala", "--some-key=val1", "--some-key=val2"))
		})

		It("should read from defaults if the user hasn't set a value for a flag", func() {
			defs := map[string][]string{
				"some-key": {"val1", "val2"},
			}
			args := EmptyArguments().
				Append("other-key", "vala", "valb")
			Expect(args.AsStrings(defs)).To(ConsistOf("--other-key=valb", "--other-key=vala", "--some-key=val1", "--some-key=val2"))
		})

		It("should not render defaults if the user has set a value for a flag", func() {
			defs := map[string][]string{
				"some-key": {"val1", "val2"},
			}
			args := EmptyArguments().
				Set("some-key", "vala")
			Expect(args.AsStrings(defs)).To(ConsistOf("--some-key=vala"))
		})
	})
})

type commaArg []string

func (a commaArg) Get(defs []string) []string {
	// not quite, but close enough
	return []string{strings.Join(defs, ",") + "," + strings.Join(a, ",")}
}
func (a commaArg) Append(vals ...string) Arg {
	return commaArg(append(a, vals...)) //nolint:unconvert
}
