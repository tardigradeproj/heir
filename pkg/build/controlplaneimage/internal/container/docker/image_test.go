package docker

import "testing"

func TestSplitImage(t *testing.T) {
	t.Parallel()
	/*
		alpine -> (alpine, latest)

		alpine:latest -> (alpine, latest)

		alpine@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913 -> (alpine, latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913)

		alpine:latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913 -> (alpine, latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913)
	*/
	cases := []struct {
		Image            string
		ExpectedRegistry string
		ExpectedTag      string
		ExpectError      bool
	}{
		{
			Image:            "alpine",
			ExpectedRegistry: "alpine",
			ExpectedTag:      "latest",
			ExpectError:      false,
		},
		{
			Image:            "alpine:latest",
			ExpectedRegistry: "alpine",
			ExpectedTag:      "latest",
			ExpectError:      false,
		},
		{
			Image:            "alpine@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectedRegistry: "alpine",
			ExpectedTag:      "latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectError:      false,
		},
		{
			Image:            "alpine:latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectedRegistry: "alpine",
			ExpectedTag:      "latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectError:      false,
		},
		{
			Image:            "registry.k8s.io/coredns:1.1.3",
			ExpectedRegistry: "registry.k8s.io/coredns",
			ExpectedTag:      "1.1.3",
			ExpectError:      false,
		},
		{
			Image:            "registry.k8s.io/coredns:1.1.3@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectedRegistry: "registry.k8s.io/coredns",
			ExpectedTag:      "1.1.3@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectError:      false,
		},
		{
			Image:            "registry.k8s.io/coredns:latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectedRegistry: "registry.k8s.io/coredns",
			ExpectedTag:      "latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectError:      false,
		},
		{
			Image:            "registry.k8s.io/coredns@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectedRegistry: "registry.k8s.io/coredns",
			ExpectedTag:      "latest@sha256:28ef97b8686a0b5399129e9b763d5b7e5ff03576aa5580d6f4182a49c5fe1913",
			ExpectError:      false,
		},
		{
			Image:            ":",
			ExpectedRegistry: "",
			ExpectedTag:      "",
			ExpectError:      true,
		},
		{
			Image:            "@",
			ExpectedRegistry: "",
			ExpectedTag:      "",
			ExpectError:      true,
		},
		{
			Image:            "a@",
			ExpectedRegistry: "",
			ExpectedTag:      "",
			ExpectError:      true,
		},
		{
			Image:            "a:",
			ExpectedRegistry: "",
			ExpectedTag:      "",
			ExpectError:      true,
		},
	}

	for _, tc := range cases {
		tc := tc // capture tc
		t.Run(tc.Image, func(t *testing.T) {
			t.Parallel()

			registry, tag, err := SplitImage(tc.Image)
			if err != nil && !tc.ExpectError {
				t.Fatalf("Unexpected error: %q", err)
			} else if err == nil && tc.ExpectError {
				t.Fatalf("Expected error but got nil")
			}
			if registry != tc.ExpectedRegistry {
				t.Fatalf("ExpectedRegistry %q != %q", tc.ExpectedRegistry, registry)
			}
			if tag != tc.ExpectedTag {
				t.Fatalf("ExpectedTag %q != %q", tc.ExpectedTag, tag)
			}
		})
	}
}
