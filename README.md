# ci-dashboard

## Requirements

- `gh`: https://cli.github.com/

  Set `GITHUB_TOKEN` environment variable:

      export GITHUB_TOKEN=$(gh auth token)

## Build & Run

To run:

    go build .

Then:

    ./ci-dashboard show cilium cilium
    ./ci-dashboard show cilium cilium-cli

To show the dashboard for a specific workflow:

    ./ci-dashboard list cilium cilium-cli

Then pick the workflow you are interested in:

    ./ci-dashboard show cilium cilium-cli -w gke.yaml
