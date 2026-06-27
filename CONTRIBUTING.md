# Contributing to heir

Thanks for taking the time to contribute to heir!

Contributing is not limited to writing code and submitting a PR. Feel free to submit an issue or comment on an existing one to report a bug, provide feedback, 
or suggest a new feature.

Of course, contributing code is more than welcome! To keep things simple, if you're fixing a small issue, you can simply submit a PR and we will pick it up. 
However, if you're planning to submit a bigger PR to implement a new feature or fix a relatively complex bug, please open an issue that explains the change 
and the motivation for it. If you're addressing a bug, please explain how to reproduce it.

If you're interested in contributing documentation, please note the following:

- Pull requests for documentation are submitted to the [heir documentation source](https://github.com/tardigradeproj/docs).

## AI Guidance

Using AI tools to help write your PR is acceptable, but as the author, you are responsible for understanding every change. If you used AI tools in preparing 
your PR, you must disclose this in the description of your PR. Listing AI tooling as a co-author, co-signing commits using an AI tool, or using the 
`assisted-by`, `co-developed-by`, or similar commit trailers is not allowed.

Large AI-generated PRs and AI-generated commit messages are not allowed. PRs with excessively large or unnecessarily scaffolded unit tests that could be 
replaced with a succinct E2E test will be closed.

Do not leave the first review of AI-generated changes to the reviewers. Verify the changes (code review, testing, etc.) before submitting your PR. Reviewers 
may ask questions about your AI-assisted code, and if you cannot explain why a change was made, the PR will be closed.

When responding to review comments, you must do so without relying on AI tools. Reviewers want to engage directly with you, not with generated responses. If 
you do not engage directly with reviewers, the PR will be closed.

## Opening PRs and organizing commits

PRs should generally address only one issue at a time. If you need to fix two bugs, open two separate PRs. This will keep the scope of your pull requests 
smaller and allow them to be reviewed and merged more quickly.

When possible, fill out as much detail in the pull request template as is reasonable. Most important is to reference the GitHub issue that you are addressing 
with the PR.

**Note:** Do not use keywords such as "Fixes" to reference issues in your PR. We don't want issues to be automatically closed, we want testers to independently 
verify and close them.

Generally, pull requests should consist of a single logical commit. If your PR is for a large feature, a more logical breakdown of commits is acceptable, 
as long as each commit represents a single logical unit.

If your PR includes changes to generated code (e.g. `zz_generated.deepcopy.go` from `make generate`), those changes should be in their own commit to make reviewing 
easier.


## Running integration tests

Integration tests provision real Kubernetes clusters and worker nodes using kind and bootloose.
They take around 30 minutes to complete and require a machine with enough resources to run several
Docker containers simultaneously (8 GB RAM minimum; 16 GB recommended).

### Prerequisites

| Tool | Purpose | Install |
|------|---------|---------|
| Go 1.26+ | Build and run tests | https://go.dev/dl |
| Docker | kind clusters and bootloose worker containers | https://docs.docker.com/get-docker |
| kind | Management cluster | `go install sigs.k8s.io/kind@latest` |
| goreleaser | Build the `heir` binary snapshot that tests consume | https://goreleaser.com/install |

### Running the tests

```bash
# 1. Build a local snapshot release — places the heir binary in dist/
make build-snapshot
# 2. Run the tests from the integration-test directory
cd integration-test && go test -v -timeout 30m .
```

### What the tests do

Each test function provisions its own upstream Kubernetes cluster (running as pods inside the
management kind cluster) and its own bootloose worker nodes. Tests are isolated by cluster name
and NodePort assignments so they can run sequentially within the same suite without conflicting.
The suite is powered by [testify/suite](https://pkg.go.dev/github.com/stretchr/testify/suite);
setup and teardown of shared infrastructure (the management cluster, the local registry, and the
shared postgres server) happen in `SetupSuite`/`TearDownSuite`.


## Reviewing, addressing feedback, and merging

Pull requests generally require two approvals from maintainers to be merged. Minor dependency updates or straightforward fixes may require only one.

When addressing review feedback, please make additional changes in new commits so reviewers can easily see what changed since their last review.

Once a PR has the necessary approvals:

- **Single logical commit** — use "Rebase and merge" to keep a clean, linear history.
- **Multiple logical commits** — use "Create a merge commit."
- **Multiple commits due to review feedback** — squash them into one (or more logical) commit(s) before merging, either via "Squash and merge" or `git rebase -i`.


```
Signed-off-by: Your Name <your@email.com>
```

You can do this automatically with `git commit -s`.
