/*
Copyright The Helm Authors.
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

/*
Package strvals provides tools for working with strval lines.

Helm supports a compressed format for YAML settings which we call strvals.
The format is roughly like this:

	name=value,topname.subname=value

The above is equivalent to the YAML document

	name: value
	topname:
	  subname: value

This package provides a parser and utilities for converting the strvals format
to other formats.

# Provenance

parser.go and literal_parser.go in this package are copied, unmodified, from
helm.sh/helm/v3/pkg/strvals at tag v3.22.0 (the Helm version pinned as
"helm" in .dependencies-version.yaml) —
https://github.com/helm/helm/tree/v3.22.0/pkg/strvals.

They are vendored here by source rather than imported as a go.mod dependency
on helm.sh/helm/v3: that module's own go.mod requires newer k8s.io/*, grpc,
and genproto versions than this repo currently uses, and importing even this
one leaf package forces Go's module resolution to bump this repo onto them.
That bump broke the build, because github.com/k3s-io/kine (used by the
tenant control plane's storage backend) is pinned to a version that doesn't
implement the newer grpc-go generated server interfaces
(it's missing the mustEmbedUnimplemented*Server methods grpc added). Copying
just the two files this repo actually uses avoids that version coupling
entirely — strvals.ParseInto's only external dependency is sigs.k8s.io/yaml,
which this repo already depends on directly.

LICENSE in this directory is Helm's own Apache License 2.0 text, covering
the copied files. Do not hand-edit parser.go or literal_parser.go; if Helm's
--set behavior needs to change here, re-copy the files from a newer tag
instead, to keep this package a clean, diffable mirror of upstream.
*/
package strvals
